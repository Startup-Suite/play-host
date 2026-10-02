package host

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Startup-Suite/play-host/internal/input"
	"github.com/Startup-Suite/play-host/internal/protocol"
	"github.com/Startup-Suite/play-host/internal/rtc"
	"github.com/pion/webrtc/v4"
)

// Task 01a0dbd6 stage 3: one encode fanned out to N pion peers, input bound
// to host-held slots. The fake core speaks the frames core's
// Platform.GameStream.Session sends a host that declares game_stream_multi.

type logRec struct {
	t     *testing.T
	mu    sync.Mutex
	lines []string
}

func (l *logRec) logf(f string, a ...any) {
	line := fmt.Sprintf(f, a...)
	l.mu.Lock()
	l.lines = append(l.lines, line)
	l.mu.Unlock()
	l.t.Log(line)
}

func (l *logRec) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

func (l *logRec) find(sub string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			out = append(out, s)
		}
	}
	return out
}

func send(h *Host, event string, v any) {
	b, _ := json.Marshal(v)
	h.OnEvent(event, b)
}

func peerOpen(h *Host, sid, pid string) {
	send(h, protocol.EventPeerOpen, protocol.PeerRef{SessionID: sid, PeerID: pid})
}

func peerClose(h *Host, sid, pid string) {
	send(h, protocol.EventPeerClose, protocol.PeerRef{SessionID: sid, PeerID: pid})
}

// slots sends play_slots in core's wire shape (src keys are strings).
func slots(h *Host, sid string, max int, peers map[string]map[string]int) {
	send(h, protocol.EventSlots, map[string]any{"session_id": sid, "max_players": max, "peers": peers})
}

// offersFor returns the offers pushed so far for peer id.
func (c *core) offersFor(id string) []protocol.Signal {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []protocol.Signal
	for _, s := range c.signals {
		if s.Kind == protocol.KindOffer && s.PeerID == id {
			out = append(out, s)
		}
	}
	return out
}

// awaitOffer waits for peer id's (n+1)th offer.
func (c *core) awaitOffer(t *testing.T, id string, n int) protocol.Signal {
	t.Helper()
	var got protocol.Signal
	eventually(t, "offer #"+fmt.Sprint(n+1)+" for "+id, func() bool {
		o := c.offersFor(id)
		if len(o) > n {
			got = o[n]
			return true
		}
		return false
	})
	return got
}

func (c *core) count(state string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, s := range c.statuses {
		if s.State == state {
			n++
		}
	}
	return n
}

func (g *fakeGame) snapshot() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.lines...)
}

// inputLines is every addon line that is not export on/off.
func inputLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if !strings.Contains(l, `"t":"export"`) {
			out = append(out, l)
		}
	}
	return out
}

type attached struct {
	pc     *webrtc.PeerConnection
	pkts   *atomic.Int64
	events *webrtc.DataChannel
	state  *webrtc.DataChannel
}

// attach answers peer id's latest offer and waits for both data channels.
func attach(t *testing.T, h *Host, c *core, id string, n int) *attached {
	t.Helper()
	offer := c.awaitOffer(t, id, n)
	if offer.PeerID != id {
		t.Fatalf("offer for %s tagged %q", id, offer.PeerID)
	}
	pc, dcs, pkts := viewer(t, h, offer)
	t.Cleanup(func() { pc.Close() })
	a := &attached{pc: pc, pkts: pkts}
	for a.events == nil || a.state == nil {
		select {
		case dc := <-dcs:
			if dc.Label() == rtc.LabelInputEvents {
				a.events = dc
			} else {
				a.state = dc
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("peer %s: data channels never opened", id)
		}
	}
	return a
}

func growing(t *testing.T, what string, p *atomic.Int64) {
	t.Helper()
	before := p.Load()
	eventually(t, what, func() bool { return p.Load() > before+3 })
}

// Three peers bind ONE track fed by ONE encoder. All three receive RTP;
// closing peer 2 leaves 1 and 3 receiving and never restarts the encoder.
// All three connections sit on the session's one UDP port.
func TestFanOutThreePeersOneEncoder(t *testing.T) {
	rtc.DisconnectGrace = 200 * time.Millisecond
	const port = 40360
	lr := &logRec{t: t}
	st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
	h, c := newTestHostLog(t, port, st, lr.logf)
	t.Cleanup(func() { stopAndWait(t, h, "m1") })
	h.OnEvent(protocol.EventSessionStart, startPayload("m1", 600))
	// Core sends these right after play_session_start, while the host builds.
	peerOpen(h, "m1", "p1")
	peerOpen(h, "m1", "p2")
	peerOpen(h, "m1", "p3")
	slots(h, "m1", 4, map[string]map[string]int{"p1": {"0": 0}, "p2": {"0": 1}, "p3": {}})

	v := map[string]*attached{}
	for _, id := range []string{"p1", "p2", "p3"} {
		v[id] = attach(t, h, c, id, 0)
	}
	for id, a := range v {
		growing(t, "RTP at "+id, a.pkts)
	}
	if n := lr.count("encoder started"); n != 1 {
		t.Fatalf("encoder started %d times for 3 peers, want 1", n)
	}
	if len(c.offersFor("")) != 0 {
		t.Fatal("a multi-peer session offered the v1 implicit peer")
	}
	// Every peer's selected pair is on the one muxed port.
	eventually(t, "3 connected", func() bool { return lr.count("viewer connected (peer p") == 3 })
	for _, l := range lr.find("viewer connected (peer p") {
		if !strings.Contains(l, fmt.Sprintf("127.0.0.1:%d <->", muxPort(port))) {
			t.Errorf("peer not on the shared port %d: %s", muxPort(port), l)
		}
	}

	peerClose(h, "m1", "p2")
	eventually(t, "p2 closed", func() bool { return v["p2"].pc.ConnectionState() != webrtc.PeerConnectionStateConnected })
	growing(t, "RTP at p1 after p2 left", v["p1"].pkts)
	growing(t, "RTP at p3 after p2 left", v["p3"].pkts)
	if n := lr.count("encoder started"); n != 1 {
		t.Fatalf("a leaver restarted the encoder: %d starts", n)
	}
	if lr.count("encoder and export stopped") != 0 {
		t.Fatal("the encoder stopped while two peers were connected")
	}
	if len(c.offersFor("p2")) != 1 {
		t.Fatal("a peer core closed was re-offered")
	}
	// live once per session; no connecting after it.
	if n := c.count(protocol.StateLive); n != 1 {
		t.Fatalf("live sent %d times", n)
	}
	if got := c.states("m1"); fmt.Sprint(got) != "[building connecting live]" {
		t.Fatalf("state sequence %v", got)
	}

	// The last two leave: the encoder stops at zero.
	peerClose(h, "m1", "p1")
	peerClose(h, "m1", "p3")
	eventually(t, "encoder stopped at 0 peers", func() bool { return lr.count("encoder and export stopped") == 1 })
}

// The forged-input rule at the session level: a spectator's data-channel
// messages (key, pad, probe, release_all; with or without a `slot`) produce
// NO addon line and are counted against its peer, and a player that names
// another slot still drives only its own.
func TestSpectatorInputNeverReachesTheAddon(t *testing.T) {
	const port = 40362
	lr := &logRec{t: t}
	st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
	h, c := newTestHostLog(t, port, st, lr.logf)
	t.Cleanup(func() { stopAndWait(t, h, "f1") })
	h.OnEvent(protocol.EventSessionStart, startPayload("f1", 600))
	peerOpen(h, "f1", "a")
	peerOpen(h, "f1", "b")
	peerOpen(h, "f1", "s")
	slots(h, "f1", 2, map[string]map[string]int{"a": {"0": 0}, "b": {"0": 1}, "s": {}})
	a := attach(t, h, c, "a", 0)
	b := attach(t, h, c, "b", 0)
	sp := attach(t, h, c, "s", 0)
	growing(t, "spectator has video", sp.pkts)

	// Let the export line settle, then snapshot.
	eventually(t, "export on", func() bool { return st.game.sawLine(`"on":true`) })
	before := len(inputLines(st.game.snapshot()))
	for _, m := range []string{
		`{"t":"key","code":"KeyD","down":true,"slot":0}`,
		`{"t":"probe","seq":5}`,
		`{"t":"release_all"}`,
		`{"t":"key","code":"KeyW","down":true,"src":1}`,
	} {
		sp.events.SendText(m)
	}
	sp.state.SendText(`{"t":"pad","slot":1,"src":0,"seq":999,"connected":true,"buttons":[1],"axes":[1,0,0,0]}`)
	eventually(t, "5 drops counted for s", func() bool { return dropsFor(h.Current(), "s") == 5 })
	// B forges P1's slot; it must land on P2 (d:1) only.
	b.events.SendText(`{"t":"key","code":"KeyD","down":true,"slot":0}`)
	eventually(t, "b's key on d:1", func() bool { return st.game.sawLine(`"d":1,"e":false,"k":"D"`) })
	// A's own key on d:0, so the addon is demonstrably reading this link.
	a.events.SendText(`{"t":"key","code":"KeyA","down":true}`)
	eventually(t, "a's key on d:0", func() bool { return st.game.sawLine(`"d":0,"e":false,"k":"A"`) })

	after := inputLines(st.game.snapshot())[before:]
	if fmt.Sprint(after) != `[{"d":1,"e":false,"k":"D","loc":0,"p":true,"t":"key"} {"d":0,"e":false,"k":"A","loc":0,"p":true,"t":"key"}]` {
		t.Fatalf("addon lines after the forgeries:\n%s", strings.Join(after, "\n"))
	}
	if got := lr.find("peer s: dropped"); len(got) == 0 || !strings.Contains(got[0], "no slot") {
		t.Fatalf("no drop line against peer s: %v", got)
	}
	// Activity dots: slots 0 and 1 saw input, and nothing named for s.
	eventually(t, "play_slot_activity", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		seen := map[int]bool{}
		for _, a := range c.activity {
			if a.SessionID != "f1" {
				return false
			}
			for _, s := range a.Slots {
				seen[s] = true
			}
		}
		return seen[0] && seen[1] && len(seen) == 2
	})
}

// A peer whose browser drops its connection is re-offered under the same
// peer_id, and a second play_peer_open for a live peer REPLACES it with a
// fresh offer at once (stage 1's H3: no waiting for the old peer to time
// out). The other peer keeps receiving throughout.
func TestReofferAndReplaceUnderTheSamePeerID(t *testing.T) {
	rtc.DisconnectGrace = 200 * time.Millisecond
	const port = 40364
	lr := &logRec{t: t}
	st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
	h, c := newTestHostLog(t, port, st, lr.logf)
	t.Cleanup(func() { stopAndWait(t, h, "r1") })
	h.OnEvent(protocol.EventSessionStart, startPayload("r1", 600))
	peerOpen(h, "r1", "p1")
	peerOpen(h, "r1", "p2")
	slots(h, "r1", 4, map[string]map[string]int{"p1": {"0": 0}, "p2": {"0": 1}})
	p1 := attach(t, h, c, "p1", 0)
	p2 := attach(t, h, c, "p2", 0)
	growing(t, "p1", p1.pkts)
	growing(t, "p2", p2.pkts)

	p1.pc.Close()
	p1b := attach(t, h, c, "p1", 1) // re-offered on its own
	growing(t, "p1 again", p1b.pkts)
	growing(t, "p2 kept receiving", p2.pkts)

	t0 := time.Now()
	peerOpen(h, "r1", "p1") // a same-page re-attach
	p1c := attach(t, h, c, "p1", 2)
	if d := time.Since(t0); d > 3*time.Second {
		t.Fatalf("replace offer took %s", d)
	}
	growing(t, "p1 replaced", p1c.pkts)
	eventually(t, "the replaced connection is closed", func() bool {
		return p1b.pc.ConnectionState() != webrtc.PeerConnectionStateConnected
	})
	if n := lr.count("encoder started"); n != 1 {
		t.Fatalf("encoder started %d times; p2 was connected throughout", n)
	}
}

// v1 compatibility: an old core sends no play_peer_open or play_slots. The
// host offers ONE implicit peer with no peer_id, src 0 is slot 0, and no
// play_slot_activity is ever pushed (an old core has no handler for it).
// A later play_peer_open switches the session to multi-peer.
func TestV1CoreGetsOneImplicitPeer(t *testing.T) {
	const port = 40366
	lr := &logRec{t: t}
	st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
	h, c := newTestHostLog(t, port, st, lr.logf)
	t.Cleanup(func() { stopAndWait(t, h, "v1") })
	h.OnEvent(protocol.EventSessionStart, startPayload("v1", 600))
	v := attach(t, h, c, "", 0)
	growing(t, "v1 video", v.pkts)
	v.events.SendText(`{"t":"key","code":"KeyW","down":true,"slot":0}`)
	v.events.SendText(`{"t":"probe","seq":4000}`)
	eventually(t, "v1 key on d:0", func() bool { return st.game.sawLine(`"d":0,"e":false,"k":"W"`) })
	eventually(t, "v1 probe seq unchanged", func() bool { return st.game.sawLine(`{"seq":4000,"t":"probe"}`) })
	time.Sleep(2 * ActivityEvery)
	c.mu.Lock()
	for _, e := range c.events {
		if e == protocol.EventSlotActivity {
			c.mu.Unlock()
			t.Fatal("play_slot_activity pushed to a v1 core")
		}
	}
	c.mu.Unlock()

	peerOpen(h, "v1", "p1")
	p1 := attach(t, h, c, "p1", 0)
	growing(t, "p1 after the switch", p1.pkts)
	eventually(t, "v1 peer closed", func() bool { return v.pc.ConnectionState() != webrtc.PeerConnectionStateConnected })
	if lr.count("closing the v1 peer") != 1 {
		t.Fatal("the switch to multi-peer did not close the implicit peer")
	}
	// Spectator until play_slots names a slot.
	before := len(inputLines(st.game.snapshot()))
	p1.events.SendText(`{"t":"key","code":"KeyS","down":true}`)
	eventually(t, "drop logged", func() bool { return lr.count("peer p1: dropped") > 0 })
	if got := inputLines(st.game.snapshot())[before:]; len(got) != 0 {
		t.Fatalf("a slotless multi peer drove the game: %v", got)
	}
	slots(h, "v1", 4, map[string]map[string]int{"p1": {"0": 2}})
	eventually(t, "slot applied", func() bool {
		p1.events.SendText(`{"t":"key","code":"KeyS","down":true}`)
		return st.game.sawLine(`"d":2,"e":false,"k":"S"`)
	})
}

// A leave (play_slots without the peer's slot) releases what it held there.
func TestLeaveReleasesTheSlot(t *testing.T) {
	const port = 40368
	st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
	h, c := newTestHost(t, port, st)
	t.Cleanup(func() { stopAndWait(t, h, "l1") })
	h.OnEvent(protocol.EventSessionStart, startPayload("l1", 600))
	peerOpen(h, "l1", "p1")
	slots(h, "l1", 4, map[string]map[string]int{"p1": {"0": 3}})
	p1 := attach(t, h, c, "p1", 0)
	p1.events.SendText(`{"t":"key","code":"KeyD","down":true}`)
	eventually(t, "held on d:3", func() bool { return st.game.sawLine(`"d":3,"e":false,"k":"D","loc":0,"p":true`) })
	slots(h, "l1", 4, map[string]map[string]int{"p1": {}})
	eventually(t, "released on leave", func() bool {
		return st.game.sawLine(`"d":3,"e":false,"k":"D","loc":0,"p":false`) && st.game.sawLine(`{"d":3,"t":"release"}`)
	})
	if st.game.sawLine(`"release_all"`) {
		t.Fatal("a leave sent the addon's global release_all")
	}
}

// dropsFor reads peer id's drop counter on the session loop.
func dropsFor(s *Session, id string) int64 {
	ch := make(chan int64, 1)
	if s == nil || !s.post(func() {
		if ps := s.peers[id]; ps != nil {
			ch <- ps.dropsTotal.Load()
		} else {
			ch <- -1
		}
	}) {
		return -1
	}
	select {
	case n := <-ch:
		return n
	case <-time.After(2 * time.Second):
		return -1
	}
}

// Touch at the session level (task 01a0fe45): a spectator's forged touch,
// with or without a `slot`, produces no addon line and is counted against
// its peer; a player's touch (the positive control) reaches the addon on its
// table slot with Godot index slot*10+id, and a leave lifts it.
func TestSpectatorTouchDroppedPlayerTouchReachesTheAddon(t *testing.T) {
	const port = 40370
	lr := &logRec{t: t}
	st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
	h, c := newTestHostLog(t, port, st, lr.logf)
	t.Cleanup(func() { stopAndWait(t, h, "t1") })
	h.OnEvent(protocol.EventSessionStart, startPayload("t1", 600))
	peerOpen(h, "t1", "p")
	peerOpen(h, "t1", "s")
	slots(h, "t1", 2, map[string]map[string]int{"p": {"0": 1}, "s": {}})
	p := attach(t, h, c, "p", 0)
	sp := attach(t, h, c, "s", 0)
	growing(t, "spectator has video", sp.pkts)
	eventually(t, "export on", func() bool { return st.game.sawLine(`"on":true`) })
	before := len(inputLines(st.game.snapshot()))

	sp.events.SendText(`{"t":"touch","src":0,"id":0,"ph":"down","x":0.5,"y":0.5}`)
	sp.events.SendText(`{"t":"touch","src":0,"id":0,"ph":"down","x":0.5,"y":0.5,"slot":0}`)
	eventually(t, "2 drops counted for s", func() bool { return dropsFor(h.Current(), "s") == 2 })

	p.events.SendText(`{"t":"touch","src":0,"id":3,"ph":"down","x":0.25,"y":0.75,"slot":0}`)
	eventually(t, "p's touch on d:1", func() bool { return st.game.sawLine(`"t":"st"`) })
	after := inputLines(st.game.snapshot())[before:]
	if fmt.Sprint(after) != `[{"c":false,"d":1,"i":13,"p":true,"t":"st","x":0.25,"y":0.75}]` {
		t.Fatalf("addon lines after the touches:\n%s", strings.Join(after, "\n"))
	}
	if got := lr.find("peer s: dropped"); len(got) == 0 || !strings.Contains(got[0], "no slot") {
		t.Fatalf("no drop line against peer s: %v", got)
	}
	if n := dropsFor(h.Current(), "p"); n != 0 {
		t.Fatalf("player's touch counted as a drop: %d", n)
	}

	// The player leaves its slot: the held touch is lifted, canceled.
	slots(h, "t1", 2, map[string]map[string]int{"p": {}, "s": {}})
	eventually(t, "touch lifted on leave", func() bool {
		return st.game.sawLine(`{"c":true,"d":1,"i":13,"p":false,"t":"st","x":0.25,"y":0.75}`)
	})
}

// Task 01a0fe45 stage 5: with LogTouch on, every st/sd line sent to the
// addon is logged once, releases included; with it off (prod), none is.
func TestLogTouchLogsEverySentTouchLine(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(fmt.Sprint("log_touch=", on), func(t *testing.T) {
			const port = 40374
			lr := &logRec{t: t}
			st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
			h, c := newTestHostLog(t, port, st, lr.logf)
			h.cfg.LogTouch = on
			t.Cleanup(func() { stopAndWait(t, h, "t1") })
			h.OnEvent(protocol.EventSessionStart, startPayload("t1", 600))
			peerOpen(h, "t1", "p")
			slots(h, "t1", 1, map[string]map[string]int{"p": {"0": 0}})
			p := attach(t, h, c, "p", 0)
			eventually(t, "export on", func() bool { return st.game.sawLine(`"on":true`) })

			p.events.SendText(`{"t":"touch","src":0,"id":2,"ph":"down","x":0.25,"y":0.5}`)
			eventually(t, "down reached the addon", func() bool { return st.game.sawLine(`"t":"st"`) })
			time.Sleep(2 * input.TouchMinInterval) // a sooner move is dropped as "touch rate"
			p.events.SendText(`{"t":"touch","src":0,"id":2,"ph":"move","x":0.3,"y":0.5}`)
			eventually(t, "drag reached the addon", func() bool { return st.game.sawLine(`"t":"sd"`) })
			p.events.SendText(`{"t":"key","src":0,"code":"KeyA","down":true}`)
			slots(h, "t1", 1, map[string]map[string]int{"p": {}})
			eventually(t, "touch lifted", func() bool { return st.game.sawLine(`"c":true,"d":0,"i":2`) })

			want := 0
			if on {
				want = 3 // st down, sd, st canceled release
			}
			if n := lr.count("touch line "); n != want {
				t.Fatalf("touch line logs = %d, want %d", n, want)
			}
			if on && lr.count(`touch line {"c":true,"d":0,"i":2,"p":false,"t":"st","x":0.3,"y":0.5}`) != 1 {
				t.Fatalf("release line not logged exactly once")
			}
		})
	}
}
