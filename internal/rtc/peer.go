// Package rtc is the play host's WebRTC side: one sendonly H.264 video track
// and two data channels, with the host creating the offer.
//
// The RTCP loop that turns PLI/FIR into OnPLI is ported from cloudplay
// (pkg/network/webrtc/webrtc.go:51-75, Apache-2.0, see NOTICE). The rest is
// written for the play host: fixed UDP port range so a single Windows firewall
// rule covers it, mDNS query mode (Chrome hides LAN host candidates behind
// .local names), and an explicit interface IP filter.
package rtc

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// Config is the network shape of the host side.
type Config struct {
	PortMin, PortMax uint16   // inclusive UDP range (40300-40309 on wave)
	AllowIPs         []net.IP // host candidate addresses; empty = all
	ICEServers       []webrtc.ICEServer
	IncludeLoopback  bool // tests only
}

// Channel labels shared with the browser.
const (
	LabelInputState  = "input-state"  // ordered:false, maxRetransmits:0; idempotent pad snapshots
	LabelInputEvents = "input-events" // reliable, ordered; key edges and probes
)

// Peer is one viewer connection.
type Peer struct {
	pc     *webrtc.PeerConnection
	Track  webrtc.TrackLocal
	OnPLI  func()
	OnData func(label string, data []byte)
	OnDone func(reason string)

	mu     sync.Mutex
	closed bool
	pli    int
}

// NewAPI builds a pion API from cfg.
func NewAPI(cfg Config) (*webrtc.API, error) {
	se := webrtc.SettingEngine{}
	if cfg.PortMin != 0 {
		if err := se.SetEphemeralUDPPortRange(cfg.PortMin, cfg.PortMax); err != nil {
			return nil, fmt.Errorf("udp port range: %w", err)
		}
	}
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeQueryOnly)
	se.SetIncludeLoopbackCandidate(cfg.IncludeLoopback)
	if len(cfg.AllowIPs) > 0 {
		allow := append([]net.IP(nil), cfg.AllowIPs...)
		se.SetIPFilter(func(ip net.IP) bool {
			for _, a := range allow {
				if a.Equal(ip) {
					return true
				}
			}
			return false
		})
	}
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	return webrtc.NewAPI(webrtc.WithSettingEngine(se), webrtc.WithMediaEngine(m)), nil
}

// NewVideoTrackRTP is a track fed with already-packetized RTP (ffmpeg -f rtp).
func NewVideoTrackRTP() (*webrtc.TrackLocalStaticRTP, error) {
	return webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
	}, "video", "play")
}

// NewVideoTrackSample is a track fed with Annex-B access units.
func NewVideoTrackSample() (*webrtc.TrackLocalStaticSample, error) {
	return webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "play")
}

// NewPeer creates the connection, adds track and data channels, and returns
// the complete (non-trickle) offer once ICE gathering finishes.
func NewPeer(api *webrtc.API, cfg Config, track webrtc.TrackLocal) (*Peer, string, error) {
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: cfg.ICEServers})
	if err != nil {
		return nil, "", err
	}
	p := &Peer{pc: pc, Track: track}
	tr, err := pc.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		pc.Close()
		return nil, "", err
	}
	go p.readRTCP(tr.Sender())

	f, zero := false, uint16(0)
	for _, dc := range []struct {
		label string
		init  *webrtc.DataChannelInit
	}{
		{LabelInputState, &webrtc.DataChannelInit{Ordered: &f, MaxRetransmits: &zero}},
		{LabelInputEvents, nil},
	} {
		ch, err := pc.CreateDataChannel(dc.label, dc.init)
		if err != nil {
			pc.Close()
			return nil, "", err
		}
		label := dc.label
		ch.OnMessage(func(m webrtc.DataChannelMessage) {
			if p.OnData != nil {
				p.OnData(label, m.Data)
			}
		})
	}
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		switch s {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			p.finish(s.String())
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		pc.Close()
		return nil, "", err
	}
	done := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		pc.Close()
		return nil, "", err
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		pc.Close()
		return nil, "", errors.New("ice gathering timed out")
	}
	return p, pc.LocalDescription().SDP, nil
}

// readRTCP is ported from cloudplay webrtc.go:51-75.
func (p *Peer) readRTCP(s *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		n, _, err := s.Read(buf)
		if err != nil {
			return
		}
		pkts, err := rtcp.Unmarshal(buf[:n])
		if err != nil {
			continue
		}
		for _, pkt := range pkts {
			switch pkt.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				p.mu.Lock()
				p.pli++
				p.mu.Unlock()
				if p.OnPLI != nil {
					p.OnPLI()
				}
			}
		}
	}
}

// PLICount is how many PLI/FIR the viewer sent.
func (p *Peer) PLICount() int { p.mu.Lock(); defer p.mu.Unlock(); return p.pli }

// SetAnswer applies the browser's answer.
func (p *Peer) SetAnswer(sdp string) error {
	return p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp})
}

// SelectedPair describes the nominated candidate pair ("host", "srflx", "relay").
func (p *Peer) SelectedPair() string {
	sctp := p.pc.SCTP()
	if sctp == nil || sctp.Transport() == nil || sctp.Transport().ICETransport() == nil {
		return ""
	}
	pair, err := sctp.Transport().ICETransport().GetSelectedCandidatePair()
	if err != nil || pair == nil {
		return ""
	}
	return fmt.Sprintf("%s %s:%d <-> %s %s:%d", pair.Local.Typ, pair.Local.Address, pair.Local.Port, pair.Remote.Typ, pair.Remote.Address, pair.Remote.Port)
}

// State is the peer connection state.
func (p *Peer) State() string { return p.pc.ConnectionState().String() }

// Close ends the connection.
func (p *Peer) Close() { p.finish("closed by host"); _ = p.pc.Close() }

func (p *Peer) finish(reason string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	if p.OnDone != nil {
		p.OnDone(reason)
	}
}
