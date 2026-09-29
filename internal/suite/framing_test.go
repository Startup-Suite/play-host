package suite

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Task 01a0dbd6 stage 6. The review saw core (Bandit 1.10.3) close the
// runtime socket with "Received unexpected binary frame (RFC6455§5.4)" right
// after the host sent a ~6.7 KB offer, which gorilla's 4096-byte default
// write buffer splits into a fragmented message. The first hypothesis was a
// second writer interleaving a frame between the fragments.
//
// strictCore is a raw RFC 6455 server that reassembles messages the way
// Bandit's Connection.handle_frame does: a text or binary frame while a
// fragmented message is pending is a protocol error, a continuation with
// none pending is one too, and control frames may interleave. It answers
// phx_join and heartbeats, and checks every s6 push arrives whole.

type strictCore struct {
	t *testing.T
	l net.Listener

	mu       sync.Mutex
	errs     []string
	msgs     int
	frames   int // data frames, continuations included
	cont     int // continuation frames
	got      map[int64]int
	beats    int
	maxFrame int
}

func newStrictCore(t *testing.T) *strictCore {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sc := &strictCore{t: t, l: l, got: map[int64]int{}}
	go sc.accept()
	t.Cleanup(func() { l.Close() })
	return sc
}

func (sc *strictCore) url() string { return "ws://" + sc.l.Addr().String() + "/runtime/ws" }

func (sc *strictCore) fail(f string, a ...any) {
	sc.mu.Lock()
	sc.errs = append(sc.errs, fmt.Sprintf(f, a...))
	sc.mu.Unlock()
}

func (sc *strictCore) accept() {
	for {
		c, err := sc.l.Accept()
		if err != nil {
			return
		}
		go sc.serve(c)
	}
}

func (sc *strictCore) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	key := req.Header.Get("Sec-WebSocket-Key")
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
		base64.StdEncoding.EncodeToString(h[:]))
	var wmu sync.Mutex
	reply := func(b []byte) {
		wmu.Lock()
		defer wmu.Unlock()
		hdr := []byte{0x81}
		switch n := len(b); {
		case n <= 125:
			hdr = append(hdr, byte(n))
		case n <= 65535:
			hdr = append(hdr, 126, byte(n>>8), byte(n))
		default:
			hdr = append(hdr, 127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
		}
		c.Write(append(hdr, b...))
	}
	var pending []byte
	fragmenting := false
	for {
		var h2 [2]byte
		if _, err := io.ReadFull(br, h2[:]); err != nil {
			return
		}
		fin, rsv, op := h2[0]&0x80 != 0, h2[0]&0x70, h2[0]&0x0f
		masked, n := h2[1]&0x80 != 0, uint64(h2[1]&0x7f)
		if rsv != 0 || !masked {
			sc.fail("bad header %x", h2)
			return
		}
		switch n {
		case 126:
			var b [2]byte
			io.ReadFull(br, b[:])
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			io.ReadFull(br, b[:])
			n = binary.BigEndian.Uint64(b[:])
		}
		var mask [4]byte
		io.ReadFull(br, mask[:])
		p := make([]byte, n)
		if _, err := io.ReadFull(br, p); err != nil {
			return
		}
		for i := range p {
			p[i] ^= mask[i%4]
		}
		if op >= 0x8 { // control frames may interleave with fragments
			if op == 0x8 {
				return
			}
			continue
		}
		sc.mu.Lock()
		sc.frames++
		if int(n) > sc.maxFrame {
			sc.maxFrame = int(n)
		}
		if op == 0 {
			sc.cont++
		}
		sc.mu.Unlock()
		switch {
		case op == 0 && !fragmenting:
			sc.fail("continuation frame with no fragmented message pending")
			return
		case op != 0 && fragmenting:
			sc.fail("unexpected opcode %d frame while a fragmented message was pending (Bandit: RFC6455§5.4)", op)
			return
		case op != 0:
			pending = append([]byte(nil), p...)
		default:
			pending = append(pending, p...)
		}
		fragmenting = !fin
		if !fin {
			continue
		}
		sc.message(pending, reply)
	}
}

func (sc *strictCore) message(b []byte, reply func([]byte)) {
	f, err := DecodeFrame(b)
	if err != nil {
		sc.fail("undecodable message (%d bytes): %v", len(b), err)
		return
	}
	sc.mu.Lock()
	sc.msgs++
	sc.mu.Unlock()
	switch {
	case f.Event == "phx_join":
		out, _ := EncodeFrame(f.JoinRef, f.Ref, f.Topic, "phx_reply", map[string]any{"status": "ok", "response": map[string]any{}})
		reply(out)
	case f.Topic == "phoenix" && f.Event == "heartbeat":
		sc.mu.Lock()
		sc.beats++
		sc.mu.Unlock()
		out, _ := EncodeFrame(nil, f.Ref, "phoenix", "phx_reply", map[string]any{"status": "ok", "response": map[string]any{}})
		reply(out)
	case f.Event == "s6":
		var p struct {
			Seq int64  `json:"seq"`
			Pad string `json:"pad"`
			N   int    `json:"n"`
		}
		if err := json.Unmarshal(f.Payload, &p); err != nil || len(p.Pad) != p.N || strings.Trim(p.Pad, "x") != "" {
			sc.fail("push %d arrived damaged", p.Seq)
			return
		}
		sc.mu.Lock()
		sc.got[p.Seq]++
		sc.mu.Unlock()
	}
}

type quietHandler struct {
	connected chan struct{}
	drops     atomic.Int64
	lastErr   atomic.Value
}

func (q *quietHandler) OnEvent(string, json.RawMessage) {}
func (q *quietHandler) OnConnected() {
	select {
	case q.connected <- struct{}{}:
	default:
	}
}
func (q *quietHandler) OnDisconnected(err error) {
	q.drops.Add(1)
	if err != nil {
		q.lastErr.Store(err.Error())
	}
}

// pushStorm joins strictCore and has `workers` goroutines push `each`
// messages of 3-20 KB (with `pace` between one worker's pushes) while a
// ticker goroutine pushes a small frame every millisecond through the same
// Push path, and the client heartbeats every `hb`. It returns what the
// server saw and the first push error.
func pushStorm(t *testing.T, writeBuf, workers, each int, pace, hb time.Duration) (*strictCore, *quietHandler) {
	t.Helper()
	sc := newStrictCore(t)
	dir := t.TempDir()
	tok := filepath.Join(dir, "token")
	os.WriteFile(tok, []byte("tok"), 0o600)
	q := &quietHandler{connected: make(chan struct{}, 1)}
	cl := New(Config{URL: sc.url(), RuntimeID: "play-host-test", TokenFile: tok, Heartbeat: hb,
		BackoffMin: time.Hour, BackoffMax: time.Hour, WriteBufferSize: writeBuf, Logf: t.Logf}, q)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { cl.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-q.connected:
	case <-time.After(5 * time.Second):
		t.Fatal("never joined")
	}
	var seq atomic.Int64
	var wg sync.WaitGroup
	var pushErr atomic.Value
	stopSmall := make(chan struct{})
	smallDone := make(chan struct{})
	go func() { // small frames, like heartbeats and ice, between the big ones
		defer close(smallDone)
		for {
			select {
			case <-stopSmall:
				return
			case <-time.After(time.Millisecond):
				cl.Push("s6_small", map[string]any{"t": time.Now().UnixNano()})
			}
		}
	}()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				n := 3000 + rand.IntN(17000)
				if err := cl.Push("s6", map[string]any{"seq": seq.Add(1), "n": n, "pad": strings.Repeat("x", n)}); err != nil {
					pushErr.CompareAndSwap(nil, err.Error())
					return
				}
				time.Sleep(pace)
			}
		}()
	}
	wg.Wait()
	close(stopSmall)
	<-smallDone
	time.Sleep(100 * time.Millisecond)
	if e := pushErr.Load(); e != nil {
		t.Logf("first push error: %v", e)
	}
	return sc, q
}

func (sc *strictCore) check(t *testing.T, q *quietHandler, want int) {
	t.Helper()
	// The server drains its socket backlog after the last Push returns.
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		sc.mu.Lock()
		n, bad := len(sc.got), len(sc.errs)
		sc.mu.Unlock()
		if n >= want || bad > 0 {
			break
		}
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if len(sc.errs) > 0 {
		t.Fatalf("server saw %d protocol error(s); first: %s", len(sc.errs), sc.errs[0])
	}
	if q.drops.Load() != 0 {
		t.Fatalf("the socket dropped: %v", q.lastErr.Load())
	}
	if len(sc.got) != want {
		t.Fatalf("server got %d distinct pushes, want %d", len(sc.got), want)
	}
	for seq, n := range sc.got {
		if n != 1 {
			t.Fatalf("push %d arrived %d times", seq, n)
		}
	}
}

// The same with real heartbeats every 150 ms, paced so the server's backlog
// stays small (a beat must be answered within one interval of being
// written): heartbeat frames land between fragmented messages, and every
// one is answered in time.
func TestHeartbeatsBetweenFragmentedPushesAreAnswered(t *testing.T) {
	sc, q := pushStorm(t, 4096, 6, 120, 8*time.Millisecond, 150*time.Millisecond)
	sc.check(t, q, 6*120)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.beats < 3 || sc.cont == 0 {
		t.Fatalf("beats %d continuations %d: the run did not interleave heartbeats with fragments", sc.beats, sc.cont)
	}
}

// Large pushes from 8 goroutines interleaved with 100 ms heartbeats, at
// gorilla's 4096-byte buffer (so nearly every push is fragmented): every
// message arrives whole and in order of frames, with no data frame inside
// another message's fragments. The client's writes are serialised by one
// mutex (write), and nothing else writes to the conn: there is no
// WriteControl, no ping handler that writes, and no second Push path.
func TestConcurrentFragmentedPushesNeverInterleave(t *testing.T) {
	sc, q := pushStorm(t, 4096, 8, 150, 0, time.Hour)
	sc.check(t, q, 8*150)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.cont == 0 {
		t.Fatal("no continuation frame: the storm never fragmented, so it tested nothing")
	}
	t.Logf("%d messages, %d data frames (%d continuations), %d heartbeats, no protocol error", sc.msgs, sc.frames, sc.cont, sc.beats)
}

// At the client's default buffer (64 KiB) every push up to core's 64 KiB
// signal cap is ONE frame: no fragmentation at all.
func TestDefaultWriteBufferSendsOneFramePerMessage(t *testing.T) {
	sc, q := pushStorm(t, 0, 4, 100, 0, time.Hour)
	sc.check(t, q, 4*100)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.cont != 0 {
		t.Fatalf("%d continuation frames at the default write buffer", sc.cont)
	}
	if sc.frames != sc.msgs {
		t.Fatalf("%d data frames for %d messages", sc.frames, sc.msgs)
	}
}
