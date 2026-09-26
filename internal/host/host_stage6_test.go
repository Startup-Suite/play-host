package host

import (
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Startup-Suite/play-host/internal/protocol"
	"github.com/Startup-Suite/play-host/internal/rtc"
	"github.com/pion/webrtc/v4"
)

// Stage 6 (task 01a0db5f): the review's findings B and C, and the checkout
// clause of case 5.

// B: repeated takeovers inside the 10-port range. Each takeover closes the
// old peer BEFORE the new one gathers, so ports are released; with the old
// asynchronous close a leaked peer would exhaust 40360-40369 within a few
// switches and the re-offer's gathering would fail.
func TestRepeatedTakeoversStayInsideThePortRange(t *testing.T) {
	rtc.DisconnectGrace = 200 * time.Millisecond
	st := &fakeStages{t: t, port: 40340, prepared: make(chan struct{})}
	h, c := newTestHost(t, 40340, st)
	h.OnEvent(protocol.EventSessionStart, startPayload("t1", 600))
	offer := <-c.offer
	for i := 0; i < 12; i++ {
		pc, _, pkts := viewer(t, h, offer)
		eventually(t, "live", func() bool { return c.last("t1").State == protocol.StateLive && pkts.Load() > 0 })
		pc.Close() // the browser tab closes: the host sees `closed` and re-offers
		select {
		case offer = <-c.offer:
		case <-time.After(10 * time.Second):
			t.Fatalf("takeover %d: no re-offer; last status %+v", i, c.last("t1"))
		}
		if s := c.last("t1").State; s == protocol.StateFailed || s == protocol.StateEnded {
			t.Fatalf("takeover %d ended the session: %+v", i, c.last("t1"))
		}
	}
	stop, _ := json.Marshal(protocol.SessionStop{SessionID: "t1", Reason: "done"})
	h.OnEvent(protocol.EventSessionStop, stop)
	eventually(t, "ended", func() bool { return c.last("t1").State == protocol.StateEnded })
}

// B: a failed offer is retried (bounded) instead of failing the session.
func TestFailedOfferIsRetried(t *testing.T) {
	var calls atomic.Int32
	real := newPeerGather
	defer func() { newPeerGather = real }()
	newPeerGather = func(api *webrtc.API, cfg rtc.Config, track webrtc.TrackLocal, cb rtc.Callbacks) (*rtc.Peer, string, rtc.Gather, error) {
		if calls.Add(1) == 1 {
			return nil, "", rtc.Gather{}, errors.New("ice gathering timed out with no host candidate")
		}
		return real(api, cfg, track, cb)
	}
	st := &fakeStages{t: t, port: 40342, prepared: make(chan struct{})}
	h, c := newTestHost(t, 40342, st)
	h.cfg.OfferBackoff = 10 * time.Millisecond
	t.Cleanup(func() { stopAndWait(t, h, "r1") })
	h.OnEvent(protocol.EventSessionStart, startPayload("r1", 600))
	select {
	case <-c.offer:
	case <-time.After(10 * time.Second):
		t.Fatalf("no offer after a failed first attempt: %+v", c.last("r1"))
	}
	if calls.Load() != 2 {
		t.Fatalf("attempts %d, want 2", calls.Load())
	}
	if s := c.last("r1").State; s != protocol.StateConnecting {
		t.Fatalf("state %s after a retried offer", s)
	}
	h.OnDisconnected(errors.New("test"))
	eventually(t, "freed", func() bool { return h.Current() == nil })
}

// B: the retry is bounded; when every attempt fails the session fails.
func TestOfferRetryIsBounded(t *testing.T) {
	var calls atomic.Int32
	real := newPeerGather
	defer func() { newPeerGather = real }()
	newPeerGather = func(api *webrtc.API, cfg rtc.Config, track webrtc.TrackLocal, cb rtc.Callbacks) (*rtc.Peer, string, rtc.Gather, error) {
		calls.Add(1)
		return nil, "", rtc.Gather{}, errors.New("boom")
	}
	st := &fakeStages{t: t, port: 40344, prepared: make(chan struct{})}
	h, c := newTestHost(t, 40344, st)
	h.cfg.OfferBackoff = 5 * time.Millisecond
	h.OnEvent(protocol.EventSessionStart, startPayload("r2", 600))
	eventually(t, "failed", func() bool { return c.last("r2").State == protocol.StateFailed })
	if calls.Load() != 3 || !strings.Contains(c.last("r2").Detail, "webrtc: boom") {
		t.Fatalf("attempts %d detail %q", calls.Load(), c.last("r2").Detail)
	}
}

// C: the crash sequence. pion fires Closed on a connection whose NewPeer
// failed (it closes the pc on its error path) while s.peer is nil after a
// viewer change. That callback used to pass the `s.peer != peer` check as
// nil == nil and run viewerGone on a nil peer: a panic on a goroutine,
// play-host.exe exit code 2, nothing logged. It must now do nothing, and
// the retry must still bring an offer.
func TestCallbackFromAFailedPeerIsIgnored(t *testing.T) {
	t.Run("a retry recovers", func(t *testing.T) { callbackFromFailedPeer(t, false, 40346) })
	// Every re-offer fails: the session fails with a status, and the queued
	// callbacks of the failed peers still do nothing (this is the path that
	// used to run viewerGone on a nil peer and crash the process).
	t.Run("all retries fail", func(t *testing.T) { callbackFromFailedPeer(t, true, 40336) })
}

func callbackFromFailedPeer(t *testing.T, failAll bool, port int) {
	var calls atomic.Int32
	real := newPeerGather
	defer func() { newPeerGather = real }()
	newPeerGather = func(api *webrtc.API, cfg rtc.Config, track webrtc.TrackLocal, cb rtc.Callbacks) (*rtc.Peer, string, rtc.Gather, error) {
		if n := calls.Add(1); n == 2 || (failAll && n > 1) { // re-offers after the first viewer leaves
			cb.OnDone("closed")
			cb.OnConnected()
			return nil, "", rtc.Gather{}, errors.New("ice gathering timed out")
		}
		return real(api, cfg, track, cb)
	}
	rtc.DisconnectGrace = 200 * time.Millisecond
	st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
	h, c := newTestHost(t, port, st)
	h.cfg.OfferBackoff = 10 * time.Millisecond
	t.Cleanup(func() { stopAndWait(t, h, "c1") })
	h.OnEvent(protocol.EventSessionStart, startPayload("c1", 600))
	offer := <-c.offer
	pc, _, pkts := viewer(t, h, offer)
	eventually(t, "live", func() bool { return pkts.Load() > 0 })
	pc.Close()
	if failAll {
		eventually(t, "failed", func() bool { return c.last("c1").State == protocol.StateFailed })
		eventually(t, "freed", func() bool { return h.Current() == nil })
		time.Sleep(100 * time.Millisecond) // let any queued callback run
		if !strings.Contains(c.last("c1").Detail, "webrtc:") {
			t.Fatalf("detail %q", c.last("c1").Detail)
		}
		return
	}
	select {
	case offer = <-c.offer:
	case <-time.After(10 * time.Second):
		t.Fatalf("no re-offer: %+v", c.last("c1"))
	}
	pc2, _, pkts2 := viewer(t, h, offer)
	t.Cleanup(func() { pc2.Close() })
	eventually(t, "live again", func() bool { return pkts2.Load() > 0 })
	if s := c.last("c1").State; s != protocol.StateLive {
		t.Fatalf("state %s", s)
	}
}

// A (host side): a second answer to one offer is logged and changes
// nothing. Core now forwards only the current viewer's answer, but the host
// must not fail the session if one ever arrives.
func TestSecondAnswerIsNotFatal(t *testing.T) {
	st := &fakeStages{t: t, port: 40348, prepared: make(chan struct{})}
	h, c := newTestHost(t, 40348, st)
	t.Cleanup(func() { stopAndWait(t, h, "a1") })
	h.OnEvent(protocol.EventSessionStart, startPayload("a1", 600))
	offer := <-c.offer
	pc, _, pkts := viewer(t, h, offer)
	defer pc.Close()
	eventually(t, "live", func() bool { return pkts.Load() > 0 })
	pc2, _, _ := viewer(t, h, offer) // a stale window answers the same offer
	defer pc2.Close()
	before := pkts.Load()
	eventually(t, "still streaming to the first viewer", func() bool { return pkts.Load() > before+3 })
	if s := c.last("a1").State; s != protocol.StateLive {
		t.Fatalf("state %s after a second answer", s)
	}
}

// Case 5: the session's checkout is removed when the session ends.
func TestCheckoutRemovedAtEnd(t *testing.T) {
	st := &fakeStages{t: t, port: 40338, prepared: make(chan struct{})}
	h, c := newTestHost(t, 40338, st)
	h.OnEvent(protocol.EventSessionStart, startPayload("k1", 1)) // idles out
	<-c.offer
	eventually(t, "idle end", func() bool { return c.last("k1").State == protocol.StateEnded })
	eventually(t, "freed", func() bool { return h.Current() == nil })
	st.mu.Lock()
	dir := st.dir
	st.mu.Unlock()
	if got := st.removedDirs(); len(got) != 1 || got[0] != dir {
		t.Fatalf("removed %v, want [%s]", got, dir)
	}
}

// stopAndWait ends a test's session and waits until the host is free, so a
// session never outlives its test (they share package-level seams).
func stopAndWait(t *testing.T, h *Host, id string) {
	stop, _ := json.Marshal(protocol.SessionStop{SessionID: id, Reason: "test over"})
	h.OnEvent(protocol.EventSessionStop, stop)
	deadline := time.Now().Add(10 * time.Second)
	for h.Current() != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}
