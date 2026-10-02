package media

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
)

// TestMain doubles as a fake ffmpeg: re-executed with FAKE_FFMPEG=1, it reads
// raw frames of the size named by -video_size from stdin and sends one RTP
// packet per frame (marker set) to the rtp:// URL, starting at a random-ish
// sequence number the way a real ffmpeg does.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_FFMPEG") == "1" {
		fakeFFmpeg()
		return
	}
	os.Exit(m.Run())
}

func fakeFFmpeg() {
	var size, url string
	for i, a := range os.Args {
		if a == "-video_size" {
			size = os.Args[i+1]
		}
		if strings.HasPrefix(a, "rtp://") {
			url = strings.TrimPrefix(strings.SplitN(a, "?", 2)[0], "rtp://")
		}
	}
	c, err := net.Dial("udp4", url)
	if err != nil {
		os.Exit(3)
	}
	seq := uint16(os.Getpid() * 7919)
	ts := uint32(os.Getpid()) * 104729
	payloadType := uint8(96)
	step := uint32(1500)
	var buf []byte
	if size == "" {
		payloadType, step = 111, 480
		buf = make([]byte, AudioChannels*4)
	} else {
		var w, h int
		fmt.Sscanf(size, "%dx%d", &w, &h)
		buf = make([]byte, w*h*4)
	}
	for {
		if _, err := io.ReadFull(os.Stdin, buf); err != nil {
			os.Exit(0)
		}
		pkt := rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: payloadType, SequenceNumber: seq, Timestamp: ts, Marker: true}, Payload: []byte{0x65, buf[0]}}
		b, _ := pkt.Marshal()
		c.Write(b)
		seq++
		ts += step
	}
}

type sink struct {
	mu   sync.Mutex
	pkts []rtp.Packet
}

func (s *sink) WriteRTP(p *rtp.Packet) error {
	s.mu.Lock()
	s.pkts = append(s.pkts, *p)
	s.mu.Unlock()
	return nil
}

func (s *sink) n() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.pkts) }

func waitN(t *testing.T, s *sink, n int) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for s.n() < n {
		if time.Now().After(end) {
			t.Fatalf("got %d packets, want %d", s.n(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPipelineRestartKeepsRTPContinuous(t *testing.T) {
	exe, _ := os.Executable()
	t.Setenv("FAKE_FFMPEG", "1")
	s := &sink{}
	p := &Pipeline{FFmpeg: exe, Preset: ControlPreset(), RTPPort: 40391, Track: s}
	if err := p.Start(64, 64); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 64*64*4)
	for i := range 3 {
		frame[0] = byte(i)
		if err := p.WriteFrame(frame); err != nil {
			t.Fatal(err)
		}
	}
	waitN(t, s, 3)
	if w, h := p.Size(); w != 64 || h != 64 || !p.Running() {
		t.Fatalf("size %dx%d running %v", w, h, p.Running())
	}
	if !strings.Contains(strings.Join(p.Args, " "), "-video_size 64x64") {
		t.Fatalf("args %v", p.Args)
	}
	p.Stop()
	if p.Running() {
		t.Fatal("still running after Stop")
	}
	// A second encoder (new viewer) starts at an unrelated seq/ts; the
	// track must see one continuous stream.
	if err := p.Start(64, 64); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		p.WriteFrame(frame)
	}
	waitN(t, s, 5)
	p.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 1; i < len(s.pkts); i++ {
		if s.pkts[i].SequenceNumber != s.pkts[i-1].SequenceNumber+1 {
			t.Errorf("seq jump at %d: %d -> %d", i, s.pkts[i-1].SequenceNumber, s.pkts[i].SequenceNumber)
		}
		if d := s.pkts[i].Timestamp - s.pkts[i-1].Timestamp; d == 0 || d > 90000*5 {
			t.Errorf("ts step at %d: %d", i, d)
		}
	}
}

func TestRewriterFirstEpochIsIdentity(t *testing.T) {
	r := NewRewriter()
	p := rtp.Packet{Header: rtp.Header{SequenceNumber: 65535, Timestamp: 4294967000}}
	r.Rewrite(&p)
	if p.SequenceNumber != 65535 || p.Timestamp != 4294967000 {
		t.Fatalf("first epoch rewritten: %+v", p.Header)
	}
	// Wraps like RTP does.
	q := rtp.Packet{Header: rtp.Header{SequenceNumber: 0, Timestamp: 1000}}
	r.Rewrite(&q)
	if q.SequenceNumber != 0 {
		t.Fatalf("wrap: %d", q.SequenceNumber)
	}
	base := time.Unix(100, 0)
	r.now = func() time.Time { return base }
	r.Rewrite(&rtp.Packet{Header: rtp.Header{SequenceNumber: 1, Timestamp: 2000}})
	r.NewEpoch()
	r.now = func() time.Time { return base.Add(100 * time.Millisecond) }
	n := rtp.Packet{Header: rtp.Header{SequenceNumber: 31337, Timestamp: 7}}
	r.Rewrite(&n)
	if n.SequenceNumber != 2 || n.Timestamp != 2000+9000 {
		t.Fatalf("new epoch: seq %d ts %d", n.SequenceNumber, n.Timestamp)
	}
}
