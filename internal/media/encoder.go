// Package media builds the ffmpeg h264_nvenc subprocess and turns its output
// into WebRTC video.
//
// The NVENC option defaults are ported from cloudplay
// (pkg/encoder/nvenc/nvenc.go:70-99, Apache-2.0, see NOTICE): CBR, an rc
// buffer of about two frames, zerolatency, no lookahead, no B-frames,
// forced IDR, baseline profile, preset p4 / tune ll. cloudplay drives
// libavcodec through cgo; v1 of the play host drives ffmpeg.exe as a
// subprocess instead, so the binary cross-compiles with CGO_ENABLED=0.
package media

import (
	"fmt"
	"strconv"
)

// Preset is one arm of the game_stream.encoder_preset experiment surface.
type Preset struct {
	Preset       string `json:"preset"`       // p1..p7
	Tune         string `json:"tune"`         // ll | ull
	BitrateKbps  int    `json:"bitrate_kbps"` // CBR target
	FPS          int    `json:"fps"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	GOPFrames    int    `json:"gop_frames"`              // bounded GOP; the subprocess cannot force an IDR on PLI
	IntraRefresh bool   `json:"intra_refresh,omitempty"` // -intra-refresh 1 instead of periodic IDR
}

// DefaultPreset is cloudplay's default arm at 720p60.
func DefaultPreset() Preset {
	return Preset{Preset: "p4", Tune: "ll", BitrateKbps: 8000, FPS: 60, Width: 1280, Height: 720, GOPFrames: 120}
}

// ControlPreset is stage 1's measured control arm and core's
// GameStreamEncoder.control/0: p1 / ll, 8000 kbps CBR, 1280x720 at 60 fps,
// GOP 120. Width and height are what the host ASKS Godot for; the session-0
// desktop clamps the window (1028x720 on wave), and the encoder is always
// sized from the frames the addon actually exports.
func ControlPreset() Preset {
	return Preset{Preset: "p1", Tune: "ll", BitrateKbps: 8000, FPS: 60, Width: 1280, Height: 720, GOPFrames: 120}
}

// Validate rejects values ffmpeg would refuse or that make no sense on a LAN.
func (p Preset) Validate() error {
	switch p.Preset {
	case "p1", "p2", "p3", "p4", "p5", "p6", "p7":
	default:
		return fmt.Errorf("preset %q not in p1..p7", p.Preset)
	}
	if p.Tune != "ll" && p.Tune != "ull" {
		return fmt.Errorf("tune %q not ll|ull", p.Tune)
	}
	if p.BitrateKbps < 500 || p.BitrateKbps > 100000 {
		return fmt.Errorf("bitrate_kbps %d out of 500..100000", p.BitrateKbps)
	}
	if p.FPS < 10 || p.FPS > 144 {
		return fmt.Errorf("fps %d out of 10..144", p.FPS)
	}
	// Parity is not checked: core's surface allows odd sizes, the size is only
	// a window request, and RawInputArgs crops odd frames to even.
	if p.Width < 64 || p.Height < 64 || p.Width > 7680 || p.Height > 4320 {
		return fmt.Errorf("size %dx%d out of 64x64..7680x4320", p.Width, p.Height)
	}
	if p.GOPFrames < 1 {
		return fmt.Errorf("gop_frames %d must be >= 1", p.GOPFrames)
	}
	return nil
}

// Label is a short stable name for logs and the spike doc.
func (p Preset) Label() string {
	s := fmt.Sprintf("%s-%s-%dk-%dp%d-g%d", p.Preset, p.Tune, p.BitrateKbps, p.Height, p.FPS, p.GOPFrames)
	if p.IntraRefresh {
		s += "-ir"
	}
	return s
}

// EncoderArgs is the output-side codec block, ported from cloudplay's defaults.
func (p Preset) EncoderArgs() []string {
	br := strconv.Itoa(p.BitrateKbps) + "k"
	// cloudplay: rc_buffer_size = bitrate/30, about two frames at 60 fps.
	buf := strconv.Itoa(max(p.BitrateKbps/30, 1)) + "k"
	a := []string{
		"-c:v", "h264_nvenc",
		"-preset", p.Preset,
		"-tune", p.Tune,
		"-rc", "cbr",
		"-b:v", br, "-maxrate", br, "-bufsize", buf,
		"-zerolatency", "1",
		"-delay", "0",
		"-rc-lookahead", "0",
		"-bf", "0",
		"-profile:v", "baseline",
		"-forced-idr", "1",
		"-g", strconv.Itoa(p.GOPFrames),
	}
	if p.IntraRefresh {
		a = append(a, "-intra-refresh", "1")
	}
	// In-band SPS/PPS before every keyframe: the browser joins mid-stream and
	// the RTP muxer would otherwise put them only in its (unused) SDP.
	a = append(a, "-bsf:v", "dump_extra=freq=keyframe")
	return a
}

// Framing is how encoded video leaves ffmpeg.
type Framing string

const (
	// FramingRTP: ffmpeg's RTP muxer on 127.0.0.1, forwarded packet-by-packet
	// to a pion TrackLocalStaticRTP. The marker bit ends a frame, so nothing
	// waits for the next frame's start code.
	FramingRTP Framing = "rtp"
	// FramingAnnexB: Annex-B on stdout read with pion's h264reader. A NAL is
	// only complete when the NEXT start code arrives, which costs up to one
	// frame interval. Kept because the plan specified it; the spike measures both.
	FramingAnnexB Framing = "annexb"
)

// OutputArgs is the muxer block for f. rtpPort is used only by FramingRTP.
func OutputArgs(f Framing, rtpPort int) []string {
	common := []string{"-an", "-flush_packets", "1"}
	switch f {
	case FramingAnnexB:
		return append(common, "-f", "h264", "pipe:1")
	default:
		return append(common, "-f", "rtp", "-payload_type", "96",
			fmt.Sprintf("rtp://127.0.0.1:%d?pkt_size=1200", rtpPort))
	}
}

// RawInputArgs reads raw frames on stdin at wall-clock timestamps (frame path C).
// NVENC accepts rgba/bgra directly and converts to YUV on the GPU.
// An odd frame size is cropped to even (a CPU filter hop, so only when needed).
func RawInputArgs(pixFmt string, w, h, fps int) []string {
	a := []string{
		"-f", "rawvideo", "-pix_fmt", pixFmt, "-video_size", fmt.Sprintf("%dx%d", w, h),
		"-framerate", strconv.Itoa(fps), "-use_wallclock_as_timestamps", "1",
		"-fflags", "nobuffer", "-flags", "low_delay", "-probesize", "32",
		"-i", "pipe:0", "-fps_mode", "passthrough",
	}
	if w%2 != 0 || h%2 != 0 {
		a = append(a, "-vf", fmt.Sprintf("crop=%d:%d:0:0", w&^1, h&^1))
	}
	return a
}

// DDAGrabInputArgs is frame path A (DXGI desktop duplication, D3D11 frames).
func DDAGrabInputArgs(fps int) []string {
	return []string{"-f", "lavfi", "-i", fmt.Sprintf("ddagrab=output_idx=0:framerate=%d:draw_mouse=0", fps)}
}

// GDIGrabInputArgs is frame path B (GDI BitBlt of one window by title).
func GDIGrabInputArgs(title string, fps int) []string {
	return []string{"-f", "gdigrab", "-framerate", strconv.Itoa(fps), "-draw_mouse", "0", "-i", "title=" + title}
}

// Command assembles a full ffmpeg argument list.
func Command(input []string, p Preset, f Framing, rtpPort int) []string {
	a := []string{"-hide_banner", "-loglevel", "warning", "-nostats", "-y"}
	a = append(a, input...)
	a = append(a, p.EncoderArgs()...)
	return append(a, OutputArgs(f, rtpPort)...)
}
