package media

import (
	"fmt"
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
		func(p *Preset) { p.Width = 10 },
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

func TestControlPresetMatchesCore(t *testing.T) {
	// Core's GameStreamEncoder @control (stage 2) and stage 1's chosen arm.
	want := Preset{Encoder: EncoderAuto, Preset: "p1", Tune: "ll", BitrateKbps: 8000, FPS: 60, Width: 1280, Height: 720, GOPFrames: 120}
	if got := ControlPreset(); got != want {
		t.Fatalf("control %+v, want %+v", got, want)
	}
	if err := want.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestOddFramesAreCroppedEven(t *testing.T) {
	if s := strings.Join(RawInputArgs("rgba", 1028, 720, 60), " "); strings.Contains(s, "-vf") {
		t.Errorf("even frame must not add a filter: %s", s)
	}
	if got := argAfter(RawInputArgs("rgba", 1027, 719, 60), "-vf"); got != "crop=1026:718:0:0" {
		t.Errorf("odd crop = %q", got)
	}
	odd := DefaultPreset()
	odd.Width = 1281
	if err := odd.Validate(); err != nil {
		t.Errorf("odd window request must validate (core allows it): %v", err)
	}
}

func TestEncoderSelectorTable(t *testing.T) {
	wantCodec := map[EncoderKind]string{
		EncoderNVENC:        "h264_nvenc",
		EncoderVideoToolbox: "h264_videotoolbox",
		EncoderLibx264:      "libx264",
	}
	if len(EncoderSelectors) != len(wantCodec) {
		t.Fatalf("selectors = %#v", EncoderSelectors)
	}
	for kind, codec := range wantCodec {
		selector, ok := EncoderSelectors[string(kind)]
		if !ok {
			t.Fatalf("missing selector %s", kind)
		}
		p := DefaultPreset()
		first, second := selector(p), selector(p)
		if !slices.Equal(first, second) || argAfter(first, "-c:v") != codec {
			t.Errorf("selector %s is not deterministic or selected %q: %v / %v", kind, codec, first, second)
		}
	}
}

func TestStartFallbackOnlyAfterFailureWhenEnabled(t *testing.T) {
	t.Run("disabled does not initialize or fall back", func(t *testing.T) {
		calls := []EncoderKind{}
		got, err := SelectStartEncoder(EncoderVideoToolbox, false, func(k EncoderKind) error {
			calls = append(calls, k)
			return fmt.Errorf("fail")
		})
		if err != nil || got != EncoderVideoToolbox || len(calls) != 0 {
			t.Fatalf("got %q, %v; calls %v", got, err, calls)
		}
	})
	t.Run("success stays selected", func(t *testing.T) {
		calls := []EncoderKind{}
		got, err := SelectStartEncoder(EncoderVideoToolbox, true, func(k EncoderKind) error {
			calls = append(calls, k)
			return nil
		})
		if err != nil || got != EncoderVideoToolbox || !slices.Equal(calls, []EncoderKind{EncoderVideoToolbox}) {
			t.Fatalf("got %q, %v; calls %v", got, err, calls)
		}
	})
	t.Run("failure initializes libx264 next", func(t *testing.T) {
		calls := []EncoderKind{}
		got, err := SelectStartEncoder(EncoderVideoToolbox, true, func(k EncoderKind) error {
			calls = append(calls, k)
			if k == EncoderVideoToolbox {
				return fmt.Errorf("hardware init failed")
			}
			return nil
		})
		if err != nil || got != EncoderLibx264 || !slices.Equal(calls, []EncoderKind{EncoderVideoToolbox, EncoderLibx264}) {
			t.Fatalf("got %q, %v; calls %v", got, err, calls)
		}
	})
}

func TestVideoToolboxLowLatencyArgs(t *testing.T) {
	got := EncoderArgs(EncoderVideoToolbox, DefaultPreset())
	want := []string{
		"-c:v", "h264_videotoolbox",
		"-realtime", "1",
		"-prio_speed", "0",
		"-profile", "baseline",
		"-b:v", "8000k",
		"-maxrate", "8000k",
		"-bufsize", "4000k",
		"-bf", "0",
		"-g", "120",
		"-pix_fmt", "nv12",
		"-bsf:v", "dump_extra=freq=keyframe",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("VideoToolbox args = %#v\nwant %#v", got, want)
	}
}

func TestLibx264LowLatencyAndRepeatHeaders(t *testing.T) {
	a := EncoderArgs(EncoderLibx264, DefaultPreset())
	if got := argAfter(a, "-c:v"); got != "libx264" {
		t.Fatalf("codec = %q", got)
	}
	if got := argAfter(a, "-tune"); got != "zerolatency" {
		t.Fatalf("tune = %q", got)
	}
	x := argAfter(a, "-x264-params")
	for _, setting := range []string{"repeat-headers=1", "keyint=120", "min-keyint=120", "scenecut=0"} {
		if !strings.Contains(x, setting) {
			t.Errorf("x264 params missing %q: %q", setting, x)
		}
	}
}

func TestAllEncodersPreserveAnnexBAndRTPJoinInvariants(t *testing.T) {
	p := DefaultPreset()
	for _, e := range []EncoderKind{EncoderNVENC, EncoderVideoToolbox, EncoderLibx264} {
		t.Run(string(e), func(t *testing.T) {
			a := EncoderArgs(e, p)
			if argAfter(a, "-bf") != "0" || argAfter(a, "-g") != "120" {
				t.Errorf("keyframe/B-frame invariant missing: %v", a)
			}
			if argAfter(a, "-bsf:v") != "dump_extra=freq=keyframe" {
				t.Errorf("SPS/PPS repeat invariant missing: %v", a)
			}
			annexB := CommandForEncoder(RawInputArgs("rgba", 1280, 720, 60), p, e, FramingAnnexB, 0)
			if !strings.HasSuffix(strings.Join(annexB, " "), "-f h264 pipe:1") {
				t.Errorf("not Annex-B output: %v", annexB)
			}
			rtp := CommandForEncoder(RawInputArgs("rgba", 1280, 720, 60), p, e, FramingRTP, 40350)
			if argAfter(rtp, "-payload_type") != "96" || !strings.Contains(strings.Join(rtp, " "), "pkt_size=1200") {
				t.Errorf("RTP contract changed: %v", rtp)
			}
		})
	}
}
