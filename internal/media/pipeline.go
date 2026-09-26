package media

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/pion/rtp"
)

// Rewriter keeps one continuous RTP sequence/timestamp space across encoder
// restarts. Every ffmpeg starts at a random sequence number and timestamp;
// the host restarts ffmpeg whenever a viewer connects (so the viewer's first
// frame is an IDR), and without this the browser would see a jump.
type Rewriter struct {
	mu       sync.Mutex
	started  bool
	fresh    bool // next packet opens a new encoder epoch
	seqOff   uint16
	tsOff    uint32
	lastSeq  uint16
	lastTS   uint32
	lastWall time.Time
	now      func() time.Time
}

// NewRewriter starts with no history.
func NewRewriter() *Rewriter { return &Rewriter{fresh: true, now: time.Now} }

// NewEpoch marks the next packet as the first of a new encoder.
func (r *Rewriter) NewEpoch() { r.mu.Lock(); r.fresh = true; r.mu.Unlock() }

// Rewrite maps pkt's sequence number and timestamp into the continuous space.
func (r *Rewriter) Rewrite(pkt *rtp.Packet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if r.fresh {
		r.fresh = false
		if r.started {
			// Continue one past the last sequence number, and advance the
			// 90 kHz clock by the wall time since the last packet (at least one tick).
			gap := uint32(max(now.Sub(r.lastWall).Microseconds()*90/1000, 1))
			r.seqOff = r.lastSeq + 1 - pkt.SequenceNumber
			r.tsOff = r.lastTS + gap - pkt.Timestamp
		}
		r.started = true
	}
	pkt.SequenceNumber += r.seqOff
	pkt.Timestamp += r.tsOff
	r.lastSeq, r.lastTS, r.lastWall = pkt.SequenceNumber, pkt.Timestamp, now
}

// Pipeline is one ffmpeg h264_nvenc subprocess fed raw RGBA frames on stdin,
// sending RTP to 127.0.0.1:RTPPort, forwarded to the viewer track. It is
// stage 1's chosen path (C readback + RTP framing).
type Pipeline struct {
	FFmpeg  string
	Preset  Preset
	RTPPort int
	Track   RTPWriter
	LogPath string
	Rewrite *Rewriter
	Counts  *Counters

	mu    sync.Mutex
	cmd   *exec.Cmd
	stdin io.WriteCloser
	conn  net.PacketConn
	w, h  int
	done  chan struct{}
	Args  []string
}

type rewriting struct {
	w  RTPWriter
	rw *Rewriter
}

func (r rewriting) WriteRTP(p *rtp.Packet) error { r.rw.Rewrite(p); return r.w.WriteRTP(p) }

// Start launches ffmpeg for w x h RGBA frames.
func (p *Pipeline) Start(w, h int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		return errors.New("pipeline already running")
	}
	if p.Rewrite == nil {
		p.Rewrite = NewRewriter()
	}
	if p.Counts == nil {
		p.Counts = &Counters{}
	}
	conn, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", p.RTPPort))
	if err != nil {
		return fmt.Errorf("rtp listen: %w", err)
	}
	p.Args = Command(RawInputArgs("rgba", w, h, p.Preset.FPS), p.Preset, FramingRTP, p.RTPPort)
	cmd := exec.Command(p.FFmpeg, p.Args...)
	var logf *os.File
	if p.LogPath != "" {
		if logf, err = os.OpenFile(p.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			fmt.Fprintf(logf, "%s ffmpeg %q\n", time.Now().UTC().Format(time.RFC3339), p.Args)
			cmd.Stderr = logf
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		conn.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		conn.Close()
		return fmt.Errorf("ffmpeg: %w", err)
	}
	p.Rewrite.NewEpoch()
	p.cmd, p.stdin, p.conn, p.w, p.h = cmd, stdin, conn, w, h
	p.done = make(chan struct{})
	done := p.done
	go func() { _ = ForwardRTP(conn, rewriting{p.Track, p.Rewrite}, p.Counts) }()
	go func() {
		_ = cmd.Wait()
		conn.Close()
		if logf != nil {
			logf.Close()
		}
		close(done)
	}()
	return nil
}

// Size is the frame size the running encoder was started for.
func (p *Pipeline) Size() (int, int) { p.mu.Lock(); defer p.mu.Unlock(); return p.w, p.h }

// Running reports whether ffmpeg is up.
func (p *Pipeline) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// WriteFrame hands one RGBA frame to ffmpeg.
func (p *Pipeline) WriteFrame(b []byte) error {
	p.mu.Lock()
	in := p.stdin
	p.mu.Unlock()
	if in == nil {
		return errors.New("pipeline not running")
	}
	_, err := in.Write(b)
	return err
}

// Stop ends ffmpeg (its own child, by handle — never by name) and waits.
func (p *Pipeline) Stop() {
	p.mu.Lock()
	cmd, in, done := p.cmd, p.stdin, p.done
	p.cmd, p.stdin = nil, nil
	p.mu.Unlock()
	if cmd == nil {
		return
	}
	in.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}
