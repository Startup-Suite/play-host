package rtc

import (
	"net"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestAudioTrackExactCapabilityAndOfferLifecycle(t *testing.T) {
	audio, err := NewAudioTrackRTP()
	if err != nil {
		t.Fatal(err)
	}
	codec := audio.Codec()
	if codec.MimeType != webrtc.MimeTypeOpus || codec.ClockRate != 48000 || codec.Channels != 2 || codec.SDPFmtpLine != "minptime=10;useinbandfec=1" {
		t.Fatalf("audio codec %+v", codec)
	}
	video, _ := NewVideoTrackRTP()
	cfg := Config{PortMin: 40393, PortMax: 40399, AllowIPs: []net.IP{net.ParseIP("127.0.0.1")}, IncludeLoopback: true}
	api, err := NewAPI(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, offer, _, err := NewPeerGatherTracks(api, cfg, []webrtc.TrackLocal{video, audio}, Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	p.Close()
	for _, want := range []string{"m=video", "m=audio", "a=sendonly", "opus/48000/2", "minptime=10;useinbandfec=1", "a=msid:play audio"} {
		if !strings.Contains(offer, want) {
			t.Errorf("offer missing %q", want)
		}
	}
}
