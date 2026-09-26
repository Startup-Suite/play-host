// Package host is the play host's session engine: it answers core's
// play_session_* frames (internal/protocol) by building the requested commit
// (internal/build), launching Godot in a Job Object (internal/launch),
// streaming it (internal/media, internal/rtc) and feeding the viewer's input
// to the suite_play addon (internal/input, internal/link).
//
// One session at a time: a start while one is running answers `failed` with
// detail "host busy" for the NEW session id and leaves the running one alone.
// A session with no input for idle_timeout_s ends with reason "idle".
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Startup-Suite/play-host/internal/build"
	"github.com/Startup-Suite/play-host/internal/input"
	"github.com/Startup-Suite/play-host/internal/launch"
	"github.com/Startup-Suite/play-host/internal/link"
	"github.com/Startup-Suite/play-host/internal/media"
	"github.com/Startup-Suite/play-host/internal/protocol"
	"github.com/Startup-Suite/play-host/internal/rtc"
	"github.com/pion/webrtc/v4"
)

// newPeerGather is rtc.NewPeerGather; tests swap it to make offers fail.
var newPeerGather = rtc.NewPeerGather

// Config is everything the engine needs about the machine.
type Config struct {
	Godot         string
	FFmpeg        string
	Build         build.Config
	LogsDir       string
	HostIP        string
	UDPMin        uint16
	UDPMax        uint16
	GodotPort     int
	RTPPort       int
	ImportTimeout time.Duration
	LinkTimeout   time.Duration
	ProgressEvery time.Duration // building/progress status cadence (5 s)
	IdleCheck     time.Duration // how often the idle timer is evaluated (1 s)
	SkipImport    bool          // tests
	Loopback      bool          // tests: offer 127.0.0.1 candidates
	// OfferAttempts bounds how many times one offer is tried before the
	// session fails (0 = 3); OfferBackoff is the wait before the first retry,
	// doubled each time (0 = 500 ms). Stage 6: one failed re-offer after a
	// viewer change used to end a live session.
	OfferAttempts int
	OfferBackoff  time.Duration
	// PionLog receives pion's ICE warnings and errors (play-host.log on wave).
	PionLog io.Writer
}

// Sender pushes host -> core frames.
type Sender interface {
	Push(event string, payload any) error
}

// Stages are swappable so the engine is testable without git or Godot.
type Stages interface {
	// Prepare returns a project dir whose HEAD is exactly start.SHA.
	Prepare(ctx context.Context, s protocol.SessionStart, progress func(detail string)) (string, error)
	// AssertHead re-checks HEAD == sha immediately before launch.
	AssertHead(ctx context.Context, dir, sha string) error
	// Launch starts Godot for dir.
	Launch(dir string, s protocol.SessionStart, logPath string) (launch.Proc, error)
	// Remove deletes the session's checkout once Godot is gone (the bare
	// mirror stays, so the next build of the same repo is still a local
	// worktree add). Stage 6: the review clause is that an ended session's
	// checkout is cleaned up.
	Remove(ctx context.Context, s protocol.SessionStart, dir string) error
}

// Host is the engine. It implements suite.Handler.
type Host struct {
	cfg    Config
	send   Sender
	stages Stages
	logf   func(string, ...any)

	mu  sync.Mutex
	cur *Session
}

// New builds an engine. stages nil means the real git + Godot stages.
func New(cfg Config, send Sender, stages Stages, logf func(string, ...any)) *Host {
	if logf == nil {
		logf = log.Printf
	}
	if cfg.ProgressEvery == 0 {
		cfg.ProgressEvery = 5 * time.Second
	}
	if cfg.IdleCheck == 0 {
		cfg.IdleCheck = time.Second
	}
	if cfg.LinkTimeout == 0 {
		cfg.LinkTimeout = 90 * time.Second
	}
	if cfg.ImportTimeout == 0 {
		cfg.ImportTimeout = 20 * time.Minute
	}
	if cfg.OfferAttempts <= 0 {
		cfg.OfferAttempts = 3
	}
	if cfg.OfferBackoff <= 0 {
		cfg.OfferBackoff = 500 * time.Millisecond
	}
	h := &Host{cfg: cfg, send: send, logf: logf}
	if stages == nil {
		stages = &realStages{h: h}
	}
	h.stages = stages
	return h
}

// SetSender wires the sender after construction (the suite client needs the
// host as its handler, and the host needs the client to push).
func (h *Host) SetSender(s Sender) { h.send = s }

// Current is the running session, or nil.
func (h *Host) Current() *Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cur
}

// SweepCheckouts removes checkouts left by a session that never ended
// cleanly (the host was killed or crashed). Call it once at start, before
// any session can run.
func (h *Host) SweepCheckouts(ctx context.Context) {
	removed, err := (&build.Builder{Cfg: h.cfg.Build, Logf: h.logf}).SweepCheckouts(ctx)
	if len(removed) > 0 || err != nil {
		h.logf("host: removed %d checkout(s) left by an earlier run: %v err=%v", len(removed), removed, err)
	}
}

// OnConnected implements suite.Handler.
func (h *Host) OnConnected() { h.logf("host: joined; ready for play_session_start") }

// OnDisconnected implements suite.Handler. Core fails the session the
// moment the channel drops ("The play host disconnected"), so the host
// tears its side down too rather than stream to a session nobody can reach.
func (h *Host) OnDisconnected(err error) {
	if s := h.Current(); s != nil {
		s.stop(fmt.Sprintf("suite connection lost: %v", err), false)
	}
}

// OnEvent implements suite.Handler.
func (h *Host) OnEvent(event string, payload json.RawMessage) {
	switch event {
	case protocol.EventSessionStart:
		h.start(payload)
	case protocol.EventSessionStop:
		var st protocol.SessionStop
		if json.Unmarshal(payload, &st) != nil {
			return
		}
		if s := h.Current(); s != nil && s.ID() == st.SessionID {
			reason := st.Reason
			if reason == "" {
				reason = "Stopped"
			}
			s.stop(reason, true)
		}
	case protocol.EventSignal:
		var sig protocol.Signal
		if json.Unmarshal(payload, &sig) != nil {
			return
		}
		if s := h.Current(); s != nil && s.ID() == sig.SessionID {
			s.signal(sig)
		}
	default:
		// capabilities, spaces manifest, etc.: not for the play host.
	}
}

func (h *Host) push(event string, payload any) {
	if h.send == nil {
		return
	}
	if err := h.send.Push(event, payload); err != nil {
		h.logf("host: push %s: %v", event, err)
	}
}

func (h *Host) start(payload json.RawMessage) {
	st, err := protocol.DecodeSessionStart(payload)
	if err != nil {
		h.logf("host: %v", err)
		var id struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(payload, &id) == nil && id.SessionID != "" {
			h.push(protocol.EventSessionStatus, protocol.NewStatus(id.SessionID, protocol.StateFailed, err.Error(), 0))
		}
		return
	}
	h.mu.Lock()
	if h.cur != nil {
		curID := h.cur.ID()
		h.mu.Unlock()
		if curID != st.SessionID {
			h.logf("host: refusing session %s: host busy with %s", st.SessionID, curID)
			h.push(protocol.EventSessionStatus, protocol.NewStatus(st.SessionID, protocol.StateFailed, "host busy", 0))
		}
		return
	}
	s := newSession(h, st)
	h.cur = s
	h.mu.Unlock()
	go func() {
		defer func() {
			// The session goroutine already fails itself on a panic (run's
			// recoverPanic); this is for a panic in its cleanup. Either way
			// the host stays up and free for the next session.
			if r := recover(); r != nil {
				h.logf("host: PANIC in session %s cleanup: %v\n%s", st.SessionID, r, debug.Stack())
			}
			h.mu.Lock()
			if h.cur == s {
				h.cur = nil
			}
			h.mu.Unlock()
		}()
		s.run()
	}()
}

// guard runs f, turning a panic into a logged session failure instead of a
// process exit. Every goroutine the session starts, and every callback pion
// runs on its own goroutines, goes through it (stage 6: an unrecovered
// panic in a pion callback killed play-host.exe with exit code 2 and
// nothing in the log).
func (s *Session) guard(where string, f func()) {
	defer s.recoverPanic(where)
	f()
}

func (s *Session) recoverPanic(where string) {
	if r := recover(); r != nil {
		s.logf("PANIC in %s: %v\n%s", where, r, debug.Stack())
		s.fail(fmt.Sprintf("The play host hit an internal error (%s); see play-host.log", where))
	}
}

// Session is one play session.
type Session struct {
	h     *Host
	start protocol.SessionStart
	t0    time.Time
	ctx   context.Context
	stopF context.CancelFunc

	mu       sync.Mutex
	state    string
	endState string
	endWhy   string
	report   bool // send the terminal status (false when the socket is gone)

	lastInput atomic.Int64 // unix nanos
	dir       string       // the checkout, removed at the end
	godot     launch.Proc
	link      *link.Link
	api       *webrtc.API
	rtcCfg    rtc.Config
	track     *webrtc.TrackLocalStaticRTP
	enc       *media.Pipeline
	peer      *rtc.Peer
	trMu      sync.Mutex
	tr        *input.Translator
	events    chan func()
	liveSent  atomic.Bool
	exporting atomic.Bool
}

func newSession(h *Host, st protocol.SessionStart) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{h: h, start: st, t0: time.Now(), ctx: ctx, stopF: cancel, report: true, events: make(chan func(), 64), tr: input.NewTranslator()}
	s.lastInput.Store(time.Now().UnixNano())
	return s
}

// ID is the session id.
func (s *Session) ID() string { return s.start.SessionID }

// State is the last state reported.
func (s *Session) State() string { s.mu.Lock(); defer s.mu.Unlock(); return s.state }

func (s *Session) logf(f string, a ...any) { s.h.logf("[%s] "+f, append([]any{s.ID()}, a...)...) }

func (s *Session) elapsed() int64 { return time.Since(s.t0).Milliseconds() }

// status reports a non-terminal state (terminal states go through finish).
func (s *Session) status(state, detail string) {
	s.mu.Lock()
	if protocol.Terminal(s.state) || s.endState != "" {
		s.mu.Unlock()
		return
	}
	s.state = state
	s.mu.Unlock()
	s.logf("status %s: %s", state, detail)
	s.h.push(protocol.EventSessionStatus, protocol.NewStatus(s.ID(), state, detail, s.elapsed()))
}

// stop ends the session from outside (core's stop, a disconnect).
func (s *Session) stop(reason string, report bool) {
	s.mu.Lock()
	if s.endState == "" {
		s.endState, s.endWhy, s.report = protocol.StateEnded, reason, report
	}
	s.mu.Unlock()
	s.stopF()
}

func (s *Session) fail(detail string) {
	s.mu.Lock()
	if s.endState == "" {
		s.endState, s.endWhy = protocol.StateFailed, detail
	}
	s.mu.Unlock()
	s.stopF()
}

func (s *Session) end(detail string) {
	s.mu.Lock()
	if s.endState == "" {
		s.endState, s.endWhy = protocol.StateEnded, detail
	}
	s.mu.Unlock()
	s.stopF()
}

func (s *Session) signal(sig protocol.Signal) {
	select {
	case s.events <- func() { s.applySignal(sig) }:
	case <-s.ctx.Done():
	}
}

func (s *Session) applySignal(sig protocol.Signal) {
	if s.peer == nil {
		s.logf("signal %s before the peer exists; dropped", sig.Kind)
		return
	}
	switch sig.Kind {
	case protocol.KindAnswer:
		d, err := sig.Description()
		if err != nil {
			s.logf("answer: %v", err)
			return
		}
		if err := s.peer.SetAnswer(d.SDP); err != nil {
			s.logf("answer: %v", err)
		}
	case protocol.KindICE:
		c, ok, err := sig.Candidate()
		if err != nil || !ok {
			return
		}
		init := webrtc.ICECandidateInit{Candidate: c.Candidate, SDPMid: c.SDPMid, SDPMLineIndex: c.SDPMLineIndex, UsernameFragment: c.UsernameFragment}
		if err := s.peer.AddICECandidate(init); err != nil {
			s.logf("ice: %v", err)
		}
	}
}

func (s *Session) run() {
	defer s.cleanup()
	defer s.recoverPanic("session")
	s.status(protocol.StateBuilding, fmt.Sprintf("Preparing %s at %s", s.start.Branch, s.start.SHA[:12]))

	// Progress reporter: building with elapsed_ms every ProgressEvery.
	var detail atomic.Value
	detail.Store("Preparing")
	progCtx, progStop := context.WithCancel(s.ctx)
	go s.guard("progress", func() {
		t := time.NewTicker(s.h.cfg.ProgressEvery)
		defer t.Stop()
		for {
			select {
			case <-progCtx.Done():
				return
			case <-t.C:
				s.status(protocol.StateBuilding, detail.Load().(string))
			}
		}
	})
	dir, err := s.h.stages.Prepare(s.ctx, s.start, func(d string) { detail.Store(d) })
	if dir != "" {
		s.dir = dir
	}
	if err != nil {
		progStop()
		s.fail("build: " + err.Error())
		return
	}
	if err := s.h.stages.AssertHead(s.ctx, dir, s.start.SHA); err != nil {
		progStop()
		s.fail(err.Error())
		return
	}
	detail.Store("Starting the game")
	godotLog := filepath.Join(s.h.cfg.LogsDir, "godot-"+s.ID()+".log")
	p, err := s.h.stages.Launch(dir, s.start, godotLog)
	if err != nil {
		progStop()
		s.fail("launch: " + err.Error())
		return
	}
	s.godot = p
	pids, _ := p.Pids()
	s.logf("godot pid %d job %v dir %s", p.Pid(), pids, dir)
	exited := make(chan int, 1)
	go s.guard("godot wait", func() {
		code, _ := p.Wait()
		exited <- code
	})
	var gone atomic.Bool
	go s.guard("godot watch", func() {
		select {
		case code := <-exited:
			gone.Store(true)
			s.fail(fmt.Sprintf("The game exited (code %d); see %s", code, filepath.Base(godotLog)))
		case <-s.ctx.Done():
		}
	})
	l, err := link.Dial(s.h.cfg.GodotPort, s.h.cfg.LinkTimeout, func() bool { return !gone.Load() && s.ctx.Err() == nil })
	progStop()
	if err != nil {
		s.fail(err.Error())
		return
	}
	s.link = l
	if err := s.setupMedia(); err != nil {
		s.fail("webrtc: " + err.Error())
		return
	}
	go s.guard("frames", s.readFrames)

	if err := s.offer(); err != nil {
		s.fail("webrtc: " + err.Error())
		return
	}
	idle := time.Duration(s.start.IdleTimeoutS) * time.Second
	tick := time.NewTicker(s.h.cfg.IdleCheck)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case f := <-s.events:
			f()
		case <-tick.C:
			if time.Since(time.Unix(0, s.lastInput.Load())) >= idle {
				s.end("idle")
				return
			}
		}
	}
}

// setupMedia builds the pion API, the one video track and the encoder
// pipeline; they outlive viewer reconnects.
func (s *Session) setupMedia() error {
	cfg := rtc.Config{PortMin: s.h.cfg.UDPMin, PortMax: s.h.cfg.UDPMax, ICEServers: ICEServers(s.start.ICEServers), IncludeLoopback: s.h.cfg.Loopback, LogWriter: s.h.cfg.PionLog}
	if s.h.cfg.HostIP != "" {
		cfg.AllowIPs = []net.IP{net.ParseIP(s.h.cfg.HostIP)}
	}
	api, err := rtc.NewAPI(cfg)
	if err != nil {
		return err
	}
	track, err := rtc.NewVideoTrackRTP()
	if err != nil {
		return err
	}
	s.api, s.rtcCfg, s.track = api, cfg, track
	s.enc = &media.Pipeline{FFmpeg: s.h.cfg.FFmpeg, Preset: s.start.Encoder, RTPPort: s.h.cfg.RTPPort, Track: track,
		LogPath: filepath.Join(s.h.cfg.LogsDir, "ffmpeg-"+s.ID()+".log"), Counts: &media.Counters{}}
	return nil
}

// post runs f on the session loop, unless the session is over.
func (s *Session) post(f func()) {
	select {
	case s.events <- f:
	case <-s.ctx.Done():
	}
}

// offer publishes a fresh viewer offer, retrying a failed attempt (bounded,
// with backoff) before giving up on the session. Stage 6: a re-offer after a
// viewer change once timed out gathering and ended a live session; one
// failed attempt is now a log line.
func (s *Session) offer() error {
	var err error
	wait := s.h.cfg.OfferBackoff
	for attempt := 1; attempt <= s.h.cfg.OfferAttempts; attempt++ {
		if err = s.newPeer(); err == nil {
			return nil
		}
		if s.ctx.Err() != nil {
			return err
		}
		s.logf("offer attempt %d/%d failed: %v", attempt, s.h.cfg.OfferAttempts, err)
		if attempt == s.h.cfg.OfferAttempts {
			break
		}
		select {
		case <-time.After(wait):
		case <-s.ctx.Done():
			return err
		}
		wait *= 2
	}
	return err
}

// newPeer creates a fresh viewer connection and publishes its offer.
func (s *Session) newPeer() error {
	s.trMu.Lock()
	s.tr = input.NewTranslator()
	s.trMu.Unlock()
	var peer *rtc.Peer
	// A callback from a peer that never became s.peer (its NewPeer failed and
	// closed it) must do nothing: `peer` is still nil then, and so may
	// s.peer be. Stage 6: that nil == nil case ran viewerGone on a nil peer
	// and panicked the process.
	mine := func() bool { return peer != nil && s.peer == peer }
	p, offer, g, err := newPeerGather(s.api, s.rtcCfg, s.track, rtc.Callbacks{
		OnPLI: func() {
			s.logf("PLI/FIR from viewer: no forced IDR in the subprocess encoder; next IDR within GOP %d", s.start.Encoder.GOPFrames)
		},
		OnData: func(label string, data []byte) {
			s.guard("input", func() { s.onInput(label, data) })
		},
		OnConnected: func() {
			s.post(func() {
				if !mine() {
					return
				}
				s.logf("viewer connected: %s", peer.SelectedPair())
				s.setExport(true)
			})
		},
		OnDone: func(reason string) {
			s.post(func() {
				if !mine() {
					return
				}
				s.logf("viewer gone: %s", reason)
				s.viewerGone()
			})
		},
	})
	if err != nil {
		return err
	}
	s.logf("offer: %s", g)
	peer = p
	s.peer = p
	sig, err := protocol.NewOfferSignal(s.ID(), offer)
	if err != nil {
		return err
	}
	s.h.push(protocol.EventSignal, sig)
	s.liveSent.Store(false)
	s.status(protocol.StateConnecting, "Waiting for the viewer")
	return nil
}

// viewerGone runs on the session loop when the current peer ends. The old
// peer is closed BEFORE the next one is created, so its UDP port in the
// 10-port range and its TURN allocation are released first (it used to be
// closed on a goroutine, racing the new peer's gathering).
func (s *Session) viewerGone() {
	old := s.peer
	if old == nil {
		return
	}
	s.peer = nil
	s.setExport(false)
	s.enc.Stop()
	s.releaseAll()
	old.Close()
	if s.ctx.Err() != nil {
		return
	}
	if err := s.offer(); err != nil {
		s.fail("webrtc: " + err.Error())
	}
}

func (s *Session) setExport(on bool) {
	s.exporting.Store(on)
	if s.link != nil {
		if err := s.link.Send(map[string]any{"t": "export", "on": on}); err != nil {
			s.logf("export %v: %v", on, err)
		}
	}
}

func (s *Session) sendLines(lines []input.Out) {
	if s.link == nil {
		return
	}
	for _, l := range lines {
		if err := s.link.Send(l); err != nil {
			s.logf("addon link send: %v", err)
			return
		}
	}
}

func (s *Session) releaseAll() {
	s.trMu.Lock()
	lines := s.tr.ReleaseAll()
	s.trMu.Unlock()
	s.sendLines(lines)
}

func (s *Session) onInput(label string, data []byte) {
	s.trMu.Lock()
	r, err := s.tr.Handle(label, data)
	s.trMu.Unlock()
	if err != nil {
		return
	}
	if r.Activity {
		s.lastInput.Store(time.Now().UnixNano())
	}
	s.sendLines(r.Lines)
}

// readFrames pumps addon frames into the encoder while exporting. The
// encoder starts on the first frame after export is switched on, sized from
// that frame (the session-0 desktop clamps the window), so a viewer's first
// frame is always an IDR.
func (s *Session) readFrames() {
	var buf []byte
	for {
		h, b, err := s.link.ReadFrame(buf)
		buf = b
		if err != nil {
			if s.ctx.Err() == nil {
				s.fail("addon link: " + err.Error())
			}
			return
		}
		if !s.exporting.Load() || h.Kind != link.KindImageRGBA8 {
			continue
		}
		if !s.enc.Running() {
			if err := s.enc.Start(int(h.Width), int(h.Height)); err != nil {
				s.fail("encoder: " + err.Error())
				return
			}
			s.logf("encoder started %dx%d %s: %s", h.Width, h.Height, s.start.Encoder.Label(), strings.Join(s.enc.Args, " "))
		}
		if err := s.enc.WriteFrame(b); err != nil {
			s.logf("encoder write: %v", err)
			continue
		}
		if s.enc.Counts.Packets.Load() > 0 && !s.liveSent.Swap(true) {
			p := s.start.Encoder
			s.status(protocol.StateLive, fmt.Sprintf("Streaming %dx%d at %d fps (%s/%s %d kbps, GOP %d); asked for %dx%d",
				h.Width, h.Height, p.FPS, p.Preset, p.Tune, p.BitrateKbps, p.GOPFrames, p.Width, p.Height))
		}
	}
}

func (s *Session) cleanup() {
	s.peer.Close() // nil-safe
	s.peer = nil
	if s.enc != nil {
		s.enc.Stop()
	}
	if s.link != nil {
		s.releaseAll()
		s.link.Close()
	}
	if s.godot != nil {
		pids, _ := s.godot.Pids()
		err := s.godot.Kill()
		s.logf("killed godot job pids=%v err=%v", pids, err)
	}
	if s.dir != "" {
		t0 := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := s.h.stages.Remove(ctx, s.start, s.dir)
		cancel()
		s.logf("removed checkout %s in %s err=%v", s.dir, time.Since(t0).Round(time.Millisecond), err)
	}
	s.mu.Lock()
	state, why, report := s.endState, s.endWhy, s.report
	if state == "" {
		state, why = protocol.StateEnded, "ended"
	}
	s.state = state
	s.mu.Unlock()
	s.logf("session %s: %s", state, why)
	if report {
		s.h.push(protocol.EventSessionStatus, protocol.NewStatus(s.ID(), state, why, s.elapsed()))
	}
}

// ICEServers converts the start payload's servers for pion.
func ICEServers(in []protocol.ICEServer) []webrtc.ICEServer {
	out := make([]webrtc.ICEServer, 0, len(in))
	for _, s := range in {
		out = append(out, webrtc.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
	}
	return out
}

// realStages uses git and Godot.
type realStages struct{ h *Host }

func (r *realStages) Prepare(ctx context.Context, st protocol.SessionStart, progress func(string)) (string, error) {
	cfg := r.h.cfg
	b := &build.Builder{Cfg: cfg.Build, Logf: r.h.logf}
	repo, err := cfg.Build.FindRepo(st.RepoURL)
	if err != nil {
		return "", err
	}
	progress("Fetching " + repo.Match)
	mirror, err := b.EnsureCommit(ctx, repo, st.SHA)
	if err != nil {
		return "", err
	}
	dir, err := build.CheckoutDir(cfg.Build.CheckoutsDir, build.Task8(st.TaskID, st.Branch, st.SessionID), st.SHA)
	if err != nil {
		return "", err
	}
	progress("Checking out " + st.SHA[:12])
	if err := b.Checkout(ctx, repo, mirror, dir, st.SHA); err != nil {
		return "", err
	}
	removed, err := b.InstallAddon(dir)
	if err != nil {
		return "", err
	}
	r.h.logf("build: %s at %s; addon installed; removed from the throwaway project.godot: %v", dir, st.SHA, removed)
	b.Prune(ctx, mirror, dir)
	if cfg.SkipImport {
		return dir, nil
	}
	progress("Importing assets")
	logPath := filepath.Join(cfg.LogsDir, "import-"+st.SessionID+".log")
	err = build.Import(ctx, cfg.Godot, dir, st.SessionID, logPath, cfg.ImportTimeout, cfg.ProgressEvery, func(el time.Duration) {
		progress(fmt.Sprintf("Importing assets (%ds)", int(el.Seconds())))
	})
	return dir, err
}

func (r *realStages) Remove(ctx context.Context, st protocol.SessionStart, dir string) error {
	cfg := r.h.cfg
	repo, err := cfg.Build.FindRepo(st.RepoURL)
	if err != nil {
		return err
	}
	b := &build.Builder{Cfg: cfg.Build, Logf: r.h.logf}
	return b.RemoveCheckout(ctx, cfg.Build.MirrorDir(repo), dir)
}

func (r *realStages) AssertHead(ctx context.Context, dir, sha string) error {
	return (&build.Builder{Cfg: r.h.cfg.Build}).AssertHead(ctx, dir, sha)
}

func (r *realStages) Launch(dir string, st protocol.SessionStart, logPath string) (launch.Proc, error) {
	cfg := r.h.cfg
	args := launch.GodotArgs(dir, st.Encoder.Width, st.Encoder.Height, st.SessionID, cfg.GodotPort, st.Encoder.FPS)
	r.h.logf("launch: %s %v", cfg.Godot, args)
	return launch.Start(launch.Spec{Path: cfg.Godot, Args: args, Dir: dir, LogPath: logPath})
}
