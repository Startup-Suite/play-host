package media

import (
	"slices"
	"strings"
	"testing"
)

func argAfter(a []string, flag string) string {
	i := slices.Index(a, flag)
	if i < 0 || i+1 >= len(a) {
		return ""
	}
	return a[i+1]
}

func TestDefaultPresetCarriesCloudplayDefaults(t *testing.T) {
	p := DefaultPreset()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	a := p.EncoderArgs()
	want := map[string]string{
		"-c:v": "h264_nvenc", "-preset": "p4", "-tune": "ll", "-rc": "cbr",
		"-b:v": "8000k", "-maxrate": "8000k", "-bufsize": "266k",
		"-zerolatency": "1", "-delay": "0", "-rc-lookahead": "0", "-bf": "0",
		"-profile:v": "baseline", "-forced-idr": "1", "-g": "120",
	}
	for k, v := range want {
		if got := argAfter(a, k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if slices.Contains(a, "-intra-refresh") {
		t.Error("intra refresh must be opt-in")
	}
}

func TestValidateRejects(t *testing.T) {
	bad := []func(*Preset){
		func(p *Preset) { p.Preset = "p8" },
		func(p *Preset) { p.Tune = "hq" },
		func(p *Preset) { p.BitrateKbps = 10 },
		func(p *Preset) { p.Width = 1281 },
		func(p *Preset) { p.GOPFrames = 0 },
		func(p *Preset) { p.FPS = 500 },
	}
	for i, mut := range bad {
		p := DefaultPreset()
		mut(&p)
		if p.Validate() == nil {
			t.Errorf("case %d: %+v validated", i, p)
		}
	}
}

func TestCommandShapes(t *testing.T) {
	p := DefaultPreset()
	p.IntraRefresh = true
	rtp := Command(RawInputArgs("rgba", 1280, 720, 60), p, FramingRTP, 40350)
	s := strings.Join(rtp, " ")
	for _, sub := range []string{"-f rawvideo -pix_fmt rgba -video_size 1280x720", "-i pipe:0", "-intra-refresh 1", "-f rtp", "rtp://127.0.0.1:40350?pkt_size=1200", "dump_extra=freq=keyframe"} {
		if !strings.Contains(s, sub) {
			t.Errorf("rtp command missing %q: %s", sub, s)
		}
	}
	ab := strings.Join(Command(GDIGrabInputArgs("win", 60), p, FramingAnnexB, 0), " ")
	if !strings.HasSuffix(ab, "-f h264 pipe:1") || !strings.Contains(ab, "-i title=win") {
		t.Errorf("annexb command: %s", ab)
	}
	if !strings.Contains(strings.Join(DDAGrabInputArgs(60), " "), "ddagrab=output_idx=0:framerate=60") {
		t.Error("ddagrab args")
	}
}

func TestLabel(t *testing.T) {
	if got := DefaultPreset().Label(); got != "p4-ll-8000k-720p60-g120" {
		t.Fatal(got)
	}
}
