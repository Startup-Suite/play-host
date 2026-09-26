package input

import (
	"encoding/json"
	"fmt"
	"testing"
)

func h(t *testing.T, tr *Translator, label, msg string) Result {
	t.Helper()
	r, err := tr.Handle(label, []byte(msg))
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
	return r
}

func js(o Out) string { b, _ := json.Marshal(o); return string(b) }

func TestKeyEdgesAndLocations(t *testing.T) {
	tr := NewTranslator()
	r := h(t, tr, "input-events", `{"t":"key","code":"KeyW","down":true}`)
	if len(r.Lines) != 1 || js(r.Lines[0]) != `{"e":false,"k":"W","loc":0,"p":true,"t":"key"}` || !r.Activity {
		t.Fatalf("down: %+v", r)
	}
	if r := h(t, tr, "input-events", `{"t":"key","code":"KeyW","down":true}`); len(r.Lines) != 0 {
		t.Fatalf("duplicate down emitted %+v", r)
	}
	if r := h(t, tr, "input-events", `{"t":"key","code":"KeyW","down":true,"repeat":true}`); len(r.Lines) != 1 || r.Lines[0]["e"] != true {
		t.Fatalf("repeat: %+v", r)
	}
	if r := h(t, tr, "input-events", `{"t":"key","code":"KeyW","down":false}`); len(r.Lines) != 1 || r.Lines[0]["p"] != false {
		t.Fatalf("up: %+v", r)
	}
	if r := h(t, tr, "input-events", `{"t":"key","code":"KeyW","down":false}`); len(r.Lines) != 0 {
		t.Fatalf("up without down emitted %+v", r)
	}
	r = h(t, tr, "input-events", `{"t":"key","code":"ShiftRight","down":true}`)
	if r.Lines[0]["k"] != "Shift" || r.Lines[0]["loc"] != LocRight {
		t.Fatalf("shift right: %+v", r)
	}
	if r := h(t, tr, "input-events", `{"t":"key","code":"IntlRo","down":true}`); len(r.Lines) != 0 || r.Activity {
		t.Fatalf("unmapped code emitted %+v", r)
	}
}

func TestKeyMapCoverage(t *testing.T) {
	want := map[string]string{
		"KeyA": "A", "KeyZ": "Z", "Digit0": "0", "Digit9": "9", "Numpad5": "Kp 5", "NumpadEnter": "Kp Enter",
		"ArrowUp": "Up", "Escape": "Escape", "Space": "Space", "Enter": "Enter", "F12": "F12",
		"Quote": "Apostrophe", "Backquote": "QuoteLeft", "Backslash": "BackSlash",
	}
	for code, name := range want {
		if keyMap[code].name != name {
			t.Errorf("%s -> %q, want %q", code, keyMap[code].name, name)
		}
	}
	if n := len(keyMap); n < 100 {
		t.Errorf("only %d codes mapped", n)
	}
}

func padMsg(seq int, buttons []float64, axes []float64) string {
	b, _ := json.Marshal(map[string]any{"t": "pad", "slot": 0, "seq": seq, "connected": true, "buttons": buttons, "axes": axes})
	return string(b)
}

func TestPadDiffEdgesOnly(t *testing.T) {
	tr := NewTranslator()
	zeros := make([]float64, 17)
	if r := h(t, tr, "input-state", padMsg(1, zeros, []float64{0, 0, 0, 0})); len(r.Lines) != 0 || r.Activity {
		t.Fatalf("idle snapshot emitted %+v", r)
	}
	b := make([]float64, 17)
	b[0] = 1    // A
	b[12] = 1   // dpad up
	b[7] = 0.75 // right trigger
	r := h(t, tr, "input-state", padMsg(2, b, []float64{0.5, -1, 0, 0}))
	got := map[string]bool{}
	for _, l := range r.Lines {
		got[js(l)] = true
	}
	for _, w := range []string{
		`{"b":0,"d":0,"p":true,"t":"jb","v":1}`,
		`{"b":11,"d":0,"p":true,"t":"jb","v":1}`,
		`{"a":5,"d":0,"t":"ja","v":0.75}`,
		`{"a":0,"d":0,"t":"ja","v":0.5}`,
		`{"a":1,"d":0,"t":"ja","v":-1}`,
	} {
		if !got[w] {
			t.Errorf("missing %s in %v", w, got)
		}
	}
	if len(r.Lines) != 5 || !r.Activity {
		t.Fatalf("lines %d", len(r.Lines))
	}
	// Same snapshot again (new seq): nothing changed, nothing sent, no activity.
	if r := h(t, tr, "input-state", padMsg(3, b, []float64{0.5, -1, 0, 0})); len(r.Lines) != 0 || r.Activity {
		t.Fatalf("unchanged snapshot emitted %+v", r)
	}
	// A late, reordered snapshot (older seq) is dropped.
	if r := h(t, tr, "input-state", padMsg(2, zeros, []float64{0, 0, 0, 0})); len(r.Lines) != 0 {
		t.Fatalf("stale snapshot applied %+v", r)
	}
	// Releasing A only.
	b[0] = 0
	r = h(t, tr, "input-state", padMsg(4, b, []float64{0.5, -1, 0, 0}))
	if len(r.Lines) != 1 || js(r.Lines[0]) != `{"b":0,"d":0,"p":false,"t":"jb","v":0}` {
		t.Fatalf("release A: %+v", r)
	}
}

func TestReleaseAllAndDisconnect(t *testing.T) {
	tr := NewTranslator()
	h(t, tr, "input-events", `{"t":"key","code":"KeyA","down":true}`)
	b := make([]float64, 17)
	b[1] = 1
	h(t, tr, "input-state", padMsg(1, b, []float64{0, 0.9, 0, 0}))
	r := h(t, tr, "input-events", `{"t":"release_all"}`)
	var s []string
	for _, l := range r.Lines {
		s = append(s, js(l))
	}
	want := []string{
		`{"e":false,"k":"A","loc":0,"p":false,"t":"key"}`,
		`{"b":1,"d":0,"p":false,"t":"jb","v":0}`,
		`{"a":1,"d":0,"t":"ja","v":0}`,
		`{"t":"release_all"}`,
	}
	if fmt.Sprint(s) != fmt.Sprint(want) {
		t.Fatalf("release_all:\n got %v\nwant %v", s, want)
	}
	// After release_all, a key-up for A is not an edge.
	if r := h(t, tr, "input-events", `{"t":"key","code":"KeyA","down":false}`); len(r.Lines) != 0 {
		t.Fatalf("%+v", r)
	}
	// A gamepad disconnect releases what it held.
	h(t, tr, "input-state", padMsg(2, b, []float64{0, 0, 0, 0}))
	r = h(t, tr, "input-state", `{"t":"pad","slot":0,"seq":3,"connected":false}`)
	if len(r.Lines) != 1 || r.Lines[0]["b"] != 1 || r.Lines[0]["p"] != false {
		t.Fatalf("disconnect: %+v", r)
	}
}

func TestSlotsAndUnknown(t *testing.T) {
	tr := NewTranslator()
	if _, err := tr.Handle("input-events", []byte(`{"t":"key","code":"KeyA","down":true,"slot":1}`)); err == nil {
		t.Fatal("slot 1 accepted in single-player v1")
	}
	old := MaxSlots
	MaxSlots = 2
	defer func() { MaxSlots = old }()
	r, err := tr.Handle("input-state", []byte(pad2(1)))
	if err != nil || len(r.Lines) != 1 || r.Lines[0]["d"] != 1 {
		t.Fatalf("slot 1 -> device 1: %+v %v", r, err)
	}
	if r, err := tr.Handle("input-events", []byte(`{"t":"mouse","x":1}`)); err != nil || len(r.Lines) != 0 {
		t.Fatalf("unknown type: %+v %v", r, err)
	}
	if _, err := tr.Handle("input-events", []byte(`{`)); err == nil {
		t.Fatal("bad json accepted")
	}
	if r := h(t, tr, "input-events", `{"t":"probe","seq":7}`); js(r.Lines[0]) != `{"seq":7,"t":"probe"}` {
		t.Fatalf("probe: %+v", r)
	}
}

func pad2(slot int) string {
	b := make([]float64, 17)
	b[3] = 1
	m, _ := json.Marshal(map[string]any{"t": "pad", "slot": slot, "seq": 1, "buttons": b, "axes": []float64{0, 0, 0, 0}})
	return string(m)
}
