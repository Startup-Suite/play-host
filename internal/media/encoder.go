// Package media builds low-latency H.264 ffmpeg commands and turns their
// output into WebRTC video.
package media

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// EncoderKind identifies an ffmpeg H.264 implementation. Auto is resolved by
// the host entry point before a pipeline starts.
type EncoderKind string

const (
	EncoderAuto         EncoderKind = "auto"
	EncoderNVENC        EncoderKind = "nvenc"
	EncoderVideoToolbox EncoderKind = "videotoolbox"
	EncoderLibx264      EncoderKind = "libx264"
)

// Validate rejects unknown encoder names. The zero value is accepted as auto
// so play_session_start frames from older versions keep working.
func (e EncoderKind) Validate() error {
	if e == "" {
		e = EncoderAuto
	}
	switch e {
	case EncoderAuto, EncoderNVENC, EncoderVideoToolbox, EncoderLibx264:
		return nil
	default:
		return fmt.Errorf("encoder %q not in auto|nvenc|videotoolbox|libx264", e)
	}
}

// Preset is one arm of the game_stream.encoder_preset experiment surface.
type Preset struct {
	Encoder      EncoderKind `json:"encoder,omitempty"` // additive; omitted means auto
	Preset       string      `json:"preset"`            // NVENC p1..p7
	Tune         string      `json:"tune"`              // NVENC ll | ull
	BitrateKbps  int         `json:"bitrate_kbps"`      // common CBR target
	FPS          int         `json:"fps"`
	Width        int         `json:"width"`
	Height       int         `json:"height"`
	GOPFrames    int         `json:"gop_frames"`
	IntraRefresh bool        `json:"intra_refresh,omitempty"`
}

func DefaultPreset() Preset {
	return Preset{Encoder: EncoderAuto, Preset: "p4", Tune: "ll", BitrateKbps: 8000, FPS: 60, Width: 1280, Height: 720, GOPFrames: 120}
}

func ControlPreset() Preset {
	return Preset{Encoder: EncoderAuto, Preset: "p1", Tune: "ll", BitrateKbps: 8000, FPS: 60, Width: 1280, Height: 720, GOPFrames: 120}
}

// EncoderKind returns the normalized kind carried by this preset.
func (p Preset) EncoderKind() EncoderKind {
	if p.Encoder == "" {
		return EncoderAuto
	}
	return p.Encoder
}

func (p Preset) Validate() error {
	if err := p.EncoderKind().Validate(); err != nil {
		return err
	}
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
	if p.Width < 64 || p.Height < 64 || p.Width > 7680 || p.Height > 4320 {
		return fmt.Errorf("size %dx%d out of 64x64..7680x4320", p.Width, p.Height)
	}
	if p.GOPFrames < 1 {
		return fmt.Errorf("gop_frames %d must be >= 1", p.GOPFrames)
	}
	return nil
}

func (p Preset) Label() string {
	s := fmt.Sprintf("%s-%s-%dk-%dp%d-g%d", p.Preset, p.Tune, p.BitrateKbps, p.Height, p.FPS, p.GOPFrames)
	if p.IntraRefresh {
		s += "-ir"
	}
	return s
}

func nvencArgs(p Preset) []string {
	br := strconv.Itoa(p.BitrateKbps) + "k"
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
	return append(a, "-bsf:v", "dump_extra=freq=keyframe")
}

func videoToolboxArgs(p Preset) []string {
	br := strconv.Itoa(p.BitrateKbps) + "k"
	buf := strconv.Itoa(max(p.BitrateKbps/2, 1)) + "k"
	return []string{
		"-c:v", "h264_videotoolbox",
		"-realtime", "1",
		"-prio_speed", "0",
		"-profile", "baseline",
		"-b:v", br,
		"-maxrate", br,
		"-bufsize", buf,
		"-bf", "0",
		"-g", strconv.Itoa(p.GOPFrames),
		"-pix_fmt", "nv12",
		"-bsf:v", "dump_extra=freq=keyframe",
	}
}

func libx264Args(p Preset) []string {
	br := strconv.Itoa(p.BitrateKbps) + "k"
	buf := strconv.Itoa(max(p.BitrateKbps/2, 1)) + "k"
	x264 := strings.Join([]string{
		"nal-hrd=cbr", "force-cfr=1", "repeat-headers=1",
		"keyint=" + strconv.Itoa(p.GOPFrames), "min-keyint=" + strconv.Itoa(p.GOPFrames), "scenecut=0",
	}, ":")
	return []string{
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-profile:v", "baseline",
		"-b:v", br,
		"-maxrate", br,
		"-bufsize", buf,
		"-bf", "0",
		"-g", strconv.Itoa(p.GOPFrames),
		"-pix_fmt", "yuv420p",
		"-x264-params", x264,
		"-bsf:v", "dump_extra=freq=keyframe",
	}
}

// EncoderSelectors is the data-driven encoder experiment surface. Each value
// is a pure Preset -> ffmpeg argv selector; changing arms requires data, not a
// branch inside EncoderArgs.
var EncoderSelectors = map[string]func(Preset) []string{
	string(EncoderNVENC):        nvencArgs,
	string(EncoderVideoToolbox): videoToolboxArgs,
	string(EncoderLibx264):      libx264Args,
}

// EncoderArgs selects a concrete encoder's arguments. Callers validate and
// resolve auto before calling it. Deliberately no encoder branching lives here.
func EncoderArgs(kind EncoderKind, p Preset) []string {
	return EncoderSelectors[string(kind)](p)
}

// EncoderArgs keeps the historic API and Windows/NVENC argv byte-for-byte.
func (p Preset) EncoderArgs() []string { return EncoderArgs(EncoderNVENC, p) }

// SelectStartEncoder performs a start-time software fallback. The chosen
// encoder is initialized first; libx264 is attempted only after that init
// fails and only when fallback is enabled.
func SelectStartEncoder(chosen EncoderKind, fallback bool, init func(EncoderKind) error) (EncoderKind, error) {
	if !fallback || chosen == EncoderLibx264 {
		return chosen, nil
	}
	if err := init(chosen); err == nil {
		return chosen, nil
	} else if fallbackErr := init(EncoderLibx264); fallbackErr != nil {
		return "", fmt.Errorf("initialize %s: %v; initialize libx264 fallback: %w", chosen, err, fallbackErr)
	}
	return EncoderLibx264, nil
}

// InitializeEncoder proves that ffmpeg can initialize a concrete encoder by
// encoding one in-memory RGBA frame. RawInputArgs deliberately asks ffmpeg for
// a tiny probe buffer and no input buffering; current ffmpeg consumes the first
// raw frame while probing, so feed three frames even though only one is encoded.
// It is used only when fallback is enabled, so the established Windows/NVENC
// startup path is unchanged.
func InitializeEncoder(ffmpeg string, p Preset, kind EncoderKind) error {
	probe := p
	probe.FPS, probe.Width, probe.Height, probe.GOPFrames = 10, 64, 64, 10
	args := []string{"-hide_banner", "-loglevel", "error", "-nostats", "-y"}
	args = append(args, RawInputArgs("rgba", probe.Width, probe.Height, probe.FPS)...)
	args = append(args, EncoderArgs(kind, probe)...)
	args = append(args, "-frames:v", "1")
	args = append(args, OutputArgs(FramingAnnexB, 0)...)
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdin = bytes.NewReader(make([]byte, probe.Width*probe.Height*4*3))
	if out, err := cmd.Output(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("ffmpeg %s init: %w: %s", kind, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return fmt.Errorf("ffmpeg %s init: %w", kind, err)
	} else if len(out) == 0 {
		return fmt.Errorf("ffmpeg %s init produced no H.264", kind)
	}
	return nil
}

type Framing string

const (
	FramingRTP    Framing = "rtp"
	FramingAnnexB Framing = "annexb"
)

func OutputArgs(f Framing, rtpPort int) []string {
	common := []string{"-an", "-flush_packets", "1"}
	switch f {
	case FramingAnnexB:
		return append(common, "-f", "h264", "pipe:1")
	default:
		return append(common, "-f", "rtp", "-payload_type", "96", fmt.Sprintf("rtp://127.0.0.1:%d?pkt_size=1200", rtpPort))
	}
}

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

func DDAGrabInputArgs(fps int) []string {
	return []string{"-f", "lavfi", "-i", fmt.Sprintf("ddagrab=output_idx=0:framerate=%d:draw_mouse=0", fps)}
}

func GDIGrabInputArgs(title string, fps int) []string {
	return []string{"-f", "gdigrab", "-framerate", strconv.Itoa(fps), "-draw_mouse", "0", "-i", "title=" + title}
}

// Command preserves the historic NVENC API.
func Command(input []string, p Preset, f Framing, rtpPort int) []string {
	return CommandForEncoder(input, p, EncoderNVENC, f, rtpPort)
}

func CommandForEncoder(input []string, p Preset, e EncoderKind, f Framing, rtpPort int) []string {
	a := []string{"-hide_banner", "-loglevel", "warning", "-nostats", "-y"}
	a = append(a, input...)
	a = append(a, EncoderArgs(e, p)...)
	return append(a, OutputArgs(f, rtpPort)...)
}
