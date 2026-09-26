package rtc

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// A pion answerer stands in for the browser: the host offer must connect on
// loopback inside the fixed port range and deliver input-events messages.
func TestOfferAnswerLoopbackDeliversInputEvents(t *testing.T) {
	cfg := Config{PortMin: 40380, PortMax: 40389, AllowIPs: []net.IP{net.ParseIP("127.0.0.1")}, IncludeLoopback: true}
	api, err := NewAPI(cfg)
	if err != nil {
		t.Fatal(err)
	}
	track, err := NewVideoTrackRTP()
	if err != nil {
		t.Fatal(err)
	}
	host, offer, err := NewPeer(api, cfg, track)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	got := make(chan string, 1)
	host.OnData = func(label string, data []byte) { got <- label + ":" + string(data) }

	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	viewer, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	labels := make(chan string, 2)
	viewer.OnDataChannel(func(dc *webrtc.DataChannel) {
		labels <- dc.Label()
		if dc.Label() == LabelInputEvents {
			dc.OnOpen(func() { _ = dc.SendText(`{"t":"probe","seq":7}`) })
		}
	})
	if err := viewer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		t.Fatal(err)
	}
	ans, err := viewer.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	g := webrtc.GatheringCompletePromise(viewer)
	if err := viewer.SetLocalDescription(ans); err != nil {
		t.Fatal(err)
	}
	<-g
	if err := host.SetAnswer(viewer.LocalDescription().SDP); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-got:
		if m != `input-events:{"t":"probe","seq":7}` {
			t.Fatalf("got %q", m)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no input-events message; host state %s", host.State())
	}
	// "host 127.0.0.1:403xx <-> ..."
	f := strings.Fields(host.SelectedPair())
	_, port, _ := net.SplitHostPort(f[1])
	if len(f) < 2 || port < "40380" || port > "40389" {
		t.Fatalf("selected pair %q outside port range", host.SelectedPair())
	}
}
