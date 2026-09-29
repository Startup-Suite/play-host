package host

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Startup-Suite/play-host/internal/protocol"
	"github.com/Startup-Suite/play-host/internal/rtc"
)

// Task 01a0dbd6 stage 6: a dropped suite socket no longer ends the session.
// The review saw the runtime socket drop twice in about 35 minutes of relay
// runs, and each drop ended the stream for every viewer, although the media
// never passes through core.

func (c *core) resumeList() []protocol.ResumeStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]protocol.ResumeStatus(nil), c.resumes...)
}

// A drop then a rejoin within the grace: the session keeps running, the
// connected peer keeps receiving the SAME stream (no new offer, no encoder
// restart), the host sends ONE resume status naming its peers, and the peer
// that was not connected is offered afresh.
func TestRejoinWithinGraceResumesTheSession(t *testing.T) {
	rtc.DisconnectGrace = 200 * time.Millisecond
	const port = 40410
	lr := &logRec{t: t}
	st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
	h, c := newTestHostLog(t, port, st, lr.logf)
	h.cfg.ResumeGrace = 400 * time.Millisecond
	t.Cleanup(func() { stopAndWait(t, h, "z1") })
	h.OnEvent(protocol.EventSessionStart, startPayload("z1", 600))
	peerOpen(h, "z1", "a")
	peerOpen(h, "z1", "b")
	slots(h, "z1", 4, map[string]map[string]int{"a": {"0": 0}, "b": {}})
	a := attach(t, h, c, "a", 0)
	growing(t, "RTP at a", a.pkts)
	c.awaitOffer(t, "b", 0) // b's offer is out; nobody answers it
	eventually(t, "live", func() bool { return c.last("z1").State == protocol.StateLive })

	h.OnDisconnected(errors.New("websocket: close 1002 (protocol error)"))
	time.Sleep(100 * time.Millisecond)
	if h.Current() == nil {
		t.Fatal("the session ended at once on a dropped socket")
	}
	growing(t, "RTP at a while the socket is down", a.pkts)
	h.OnConnected()

	eventually(t, "one resume status", func() bool { return len(c.resumeList()) == 1 })
	r := c.resumeList()[0]
	if r.SessionID != "z1" || r.State != protocol.StateLive || fmt.Sprint(r.Peers) != "[a b]" {
		t.Fatalf("resume %+v", r)
	}
	if !strings.HasPrefix(r.Detail, "Streaming") {
		t.Fatalf("resume detail %q, want the last live detail", r.Detail)
	}
	c.awaitOffer(t, "b", 1) // the unconnected peer is re-offered
	// Past the grace the session still runs, and a was never re-offered.
	time.Sleep(600 * time.Millisecond)
	if h.Current() == nil {
		t.Fatal("the grace timer ended a resumed session")
	}
	growing(t, "RTP at a after the resume", a.pkts)
	if n := len(c.offersFor("a")); n != 1 {
		t.Fatalf("the connected peer was re-offered: %d offers", n)
	}
	if n := lr.count("encoder started"); n != 1 {
		t.Fatalf("encoder started %d times", n)
	}
	if n := len(c.resumeList()); n != 1 {
		t.Fatalf("%d resume statuses", n)
	}
}

// No rejoin within the grace: the session ends, with no status (the socket
// is gone), and a later rejoin sends no resume.
func TestNoRejoinWithinGraceEndsTheSession(t *testing.T) {
	const port = 40412
	lr := &logRec{t: t}
	st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
	h, c := newTestHostLog(t, port, st, lr.logf)
	h.cfg.ResumeGrace = 150 * time.Millisecond
	h.OnEvent(protocol.EventSessionStart, startPayload("z2", 600))
	<-c.offer
	h.OnDisconnected(errors.New("heartbeat reply missed"))
	time.Sleep(60 * time.Millisecond)
	if h.Current() == nil {
		t.Fatal("ended before the grace ran out")
	}
	eventually(t, "session ended", func() bool { return h.Current() == nil })
	if lr.count("not back within 150ms") == 0 {
		t.Fatal("the end does not say the socket was not back in time")
	}
	if s := c.last("z2").State; protocol.Terminal(s) {
		t.Fatalf("a %s status was pushed with the socket gone", s)
	}
	h.OnConnected()
	time.Sleep(50 * time.Millisecond)
	if n := len(c.resumeList()); n != 0 {
		t.Fatalf("%d resume statuses for an ended session", n)
	}
}

// A second drop during the grace restarts it; the first timer does nothing.
func TestASecondDropRestartsTheGrace(t *testing.T) {
	const port = 40414
	st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
	h, c := newTestHost(t, port, st)
	h.cfg.ResumeGrace = 200 * time.Millisecond
	t.Cleanup(func() { stopAndWait(t, h, "z3") })
	h.OnEvent(protocol.EventSessionStart, startPayload("z3", 600))
	<-c.offer
	h.OnDisconnected(errors.New("drop 1"))
	time.Sleep(120 * time.Millisecond)
	h.OnConnected()
	h.OnDisconnected(errors.New("drop 2"))
	time.Sleep(150 * time.Millisecond) // past drop 1's deadline, inside drop 2's
	if h.Current() == nil {
		t.Fatal("the first drop's timer ended the session")
	}
	h.OnConnected()
	time.Sleep(300 * time.Millisecond)
	if h.Current() == nil {
		t.Fatal("a resumed session ended")
	}
	eventually(t, "two resumes", func() bool { return len(c.resumeList()) == 2 })
}

// A negative grace keeps the pre-stage-6 behaviour: end at once. Shutdown
// always ends at once.
func TestNegativeGraceAndShutdownEndAtOnce(t *testing.T) {
	const port = 40416
	st := &fakeStages{t: t, port: port, prepared: make(chan struct{})}
	h, c := newTestHost(t, port, st)
	h.cfg.ResumeGrace = -1
	h.OnEvent(protocol.EventSessionStart, startPayload("z4", 600))
	<-c.offer
	h.OnDisconnected(errors.New("drop"))
	eventually(t, "ended", func() bool { return h.Current() == nil })

	st2 := &fakeStages{t: t, port: 40418, prepared: make(chan struct{})}
	h2, c2 := newTestHost(t, 40418, st2)
	h2.cfg.ResumeGrace = time.Hour
	h2.OnEvent(protocol.EventSessionStart, startPayload("z5", 600))
	<-c2.offer
	h2.OnDisconnected(errors.New("drop"))
	h2.Shutdown("play-host stopping")
	eventually(t, "shut down", func() bool { return h2.Current() == nil })
}
