package input

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"
)

func h(t *testing.T, tr *Binding, label, msg string) Result {
	t.Helper()
	r, err := tr.Handle(label, []byte(msg))
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
	return r
}

func js(o Out) string { b, _ := json.Marshal(o); return string(b) }

func TestKeyEdgesAndLocations(t *testing.T) {
	tr := NewV1Binding()
	r := h(t, tr, "input-events", `{"t":"key","code":"KeyW","down":true}`)
	if len(r.Lines) != 1 || js(r.Lines[0]) != `{"d":0,"e":false,"k":"W","loc":0,"p":true,"t":"key"}` || !r.Activity {
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
	tr := NewV1Binding()
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
	tr := NewV1Binding()
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
		`{"d":0,"e":false,"k":"A","loc":0,"p":false,"t":"key"}`,
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

// v1 (an old core, no play_slots): the implicit peer's src 0 is slot 0, and
// a `slot` the browser sends is not read at all (it used to be range-checked
// against the package-global MaxSlots, which is gone).
func TestV1BindingAndUnknown(t *testing.T) {
	tr := NewV1Binding()
	r, err := tr.Handle("input-state", []byte(pad2(1)))
	if err != nil || len(r.Lines) != 1 || r.Lines[0]["d"] != 0 || r.Slot != 0 {
		t.Fatalf("v1 slot:1 must bind to slot 0: %+v %v", r, err)
	}
	if r, err := tr.Handle("input-events", []byte(`{"t":"mouse","x":1}`)); err != nil || len(r.Lines) != 0 || r.Dropped != "" {
		t.Fatalf("unknown type: %+v %v", r, err)
	}
	if _, err := tr.Handle("input-events", []byte(`{`)); err == nil {
		t.Fatal("bad json accepted")
	}
	// v1 forwards the probe seq unchanged, all 12 bits.
	if r := h(t, tr, "input-events", `{"t":"probe","seq":4000}`); js(r.Lines[0]) != `{"seq":4000,"t":"probe"}` {
		t.Fatalf("probe: %+v", r)
	}
	// A src the v1 table does not hold is dropped, like any unbound src.
	if r := h(t, tr, "input-events", `{"t":"key","code":"KeyA","down":true,"src":1}`); r.Dropped != DropNoSlot || len(r.Lines) != 0 {
		t.Fatalf("v1 src 1: %+v", r)
	}
	if lines := tr.SetSlots(map[int]int{}); lines != nil || len(tr.Slots()) != 1 {
		t.Fatalf("a v1 binding must ignore play_slots: %v %v", lines, tr.Slots())
	}
}
func pad2(slot int) string {
	b := make([]float64, 17)
	b[3] = 1
	m, _ := json.Marshal(map[string]any{"t": "pad", "slot": slot, "seq": 1, "buttons": b, "axes": []float64{0, 0, 0, 0}})
	return string(m)
}

// fakeClock replaces a translator's clock; each call to step advances it.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time       { return c.t }
func (c *fakeClock) step(d time.Duration) { c.t = c.t.Add(d) }
func clocked(b *Binding) *fakeClock       { c := &fakeClock{t: time.Unix(1, 0)}; b.tr.now = c.now; return c }
func touch(id int, ph string, x, y float64) string {
	return fmt.Sprintf(`{"t":"touch","src":0,"id":%d,"ph":%q,"x":%v,"y":%v}`, id, ph, x, y)
}

// Touch encode table (task 01a0fe45, core protocol.ex "Touch"): down is st
// pressed, move is sd, up is st released, cancel is st canceled, and the
// Godot index is slot*10+id.
func TestTouchEncodeTable(t *testing.T) {
	b := NewBinding()
	b.SetSlots(map[int]int{0: 2})
	clk := clocked(b)
	rows := []struct{ msg, want string }{
		{touch(0, "down", 0.42, 0.77), `{"c":false,"d":2,"i":20,"p":true,"t":"st","x":0.42,"y":0.77}`},
		{touch(0, "move", 0.45, 0.7), `{"d":2,"i":20,"t":"sd","x":0.45,"y":0.7}`},
		{touch(0, "down", 0.5, 0.6), `{"d":2,"i":20,"t":"sd","x":0.5,"y":0.6}`}, // down on a held id is a move
		{touch(3, "down", 0, 1), `{"c":false,"d":2,"i":23,"p":true,"t":"st","x":0,"y":1}`},
		{touch(0, "up", 0.5, 0.61), `{"c":false,"d":2,"i":20,"p":false,"t":"st","x":0.5,"y":0.61}`},
		{touch(3, "cancel", 0.1, 0.9), `{"c":true,"d":2,"i":23,"p":false,"t":"st","x":0.1,"y":0.9}`},
		{touch(9, "down", 1, 0), `{"c":false,"d":2,"i":29,"p":true,"t":"st","x":1,"y":0}`},
	}
	for _, r := range rows {
		clk.step(10 * time.Millisecond)
		got := h(t, b, "input-events", r.msg)
		if lines(got) != r.want || !got.Activity || got.Slot != 2 || got.Dropped != "" {
			t.Errorf("%s:\n got %q %+v\nwant %q", r.msg, lines(got), got, r.want)
		}
	}
}

// Malformed touches are dropped as DropBadTouch, never as activity, and a
// move/up/cancel on an id the slot does not hold is ignored (no line, no
// drop), like a key up without a down.
func TestTouchValidationAndUnheld(t *testing.T) {
	b := NewBinding()
	b.SetSlots(map[int]int{0: 0})
	clk := clocked(b)
	bad := []string{
		`{"t":"touch","src":0,"id":10,"ph":"down","x":0.5,"y":0.5}`,
		`{"t":"touch","src":0,"id":-1,"ph":"down","x":0.5,"y":0.5}`,
		`{"t":"touch","src":0,"id":0,"ph":"hover","x":0.5,"y":0.5}`,
		`{"t":"touch","src":0,"id":0,"x":0.5,"y":0.5}`,
		`{"t":"touch","src":0,"id":0,"ph":"down","y":0.5}`,
		`{"t":"touch","src":0,"id":0,"ph":"down","x":0.5}`,
		`{"t":"touch","src":0,"id":0,"ph":"down","x":1.0001,"y":0.5}`,
		`{"t":"touch","src":0,"id":0,"ph":"down","x":0.5,"y":-0.01}`,
		`{"t":"touch","src":0,"id":0,"ph":"down","x":null,"y":0.5}`,
	}
	for _, m := range bad {
		clk.step(10 * time.Millisecond)
		if r := h(t, b, "input-events", m); r.Dropped != DropBadTouch || len(r.Lines) != 0 || r.Activity {
			t.Errorf("%s: %+v", m, r)
		}
	}
	// NaN and Inf cannot be spelled in JSON, so check them on Touch itself.
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		v, ok := v, 0.5
		for _, m := range []Browser{{T: "touch", Ph: "down", X: &v, Y: &ok}, {T: "touch", Ph: "down", X: &ok, Y: &v}} {
			if r := b.tr.Touch(m, 0); r.Dropped != DropBadTouch || len(r.Lines) != 0 {
				t.Errorf("%v: %+v", v, r)
			}
		}
	}
	for _, ph := range []string{"move", "up", "cancel"} {
		clk.step(10 * time.Millisecond)
		if r := h(t, b, "input-events", touch(4, ph, 0.5, 0.5)); len(r.Lines) != 0 || r.Dropped != "" || r.Activity {
			t.Errorf("unheld %s: %+v", ph, r)
		}
	}
	// Nothing above left a touch held: a release lifts nothing.
	if got := b.Release(); fmt.Sprint(got) != `[map[d:0 t:release]]` {
		t.Fatalf("release after only bad/unheld touches: %v", got)
	}
}

// The rate bound: a move for a (slot, id) under 4 ms after the previous
// line for it is dropped as DropTouchRate; another id, or the same id 4 ms
// on, passes. up is never rate-bounded.
func TestTouchRateBound(t *testing.T) {
	b := NewBinding()
	b.SetSlots(map[int]int{0: 0})
	clk := clocked(b)
	h(t, b, "input-events", touch(0, "down", 0.1, 0.1))
	h(t, b, "input-events", touch(1, "down", 0.2, 0.2))
	clk.step(TouchMinInterval - time.Microsecond)
	if r := h(t, b, "input-events", touch(0, "move", 0.3, 0.3)); r.Dropped != DropTouchRate || len(r.Lines) != 0 || r.Activity {
		t.Fatalf("fast move: %+v", r)
	}
	if r := h(t, b, "input-events", touch(0, "down", 0.3, 0.3)); r.Dropped != DropTouchRate {
		t.Fatalf("fast down-as-move: %+v", r)
	}
	clk.step(time.Microsecond)
	if r := h(t, b, "input-events", touch(0, "move", 0.3, 0.3)); lines(r) != `{"d":0,"i":0,"t":"sd","x":0.3,"y":0.3}` {
		t.Fatalf("move at 4 ms: %+v", r)
	}
	if r := h(t, b, "input-events", touch(1, "move", 0.4, 0.4)); lines(r) != `{"d":0,"i":1,"t":"sd","x":0.4,"y":0.4}` {
		t.Fatalf("other id at its own 4 ms: %+v", r)
	}
	// Immediately after an accepted move, a second one is bounded again,
	// but an up is not.
	if r := h(t, b, "input-events", touch(0, "move", 0.35, 0.35)); r.Dropped != DropTouchRate {
		t.Fatalf("second fast move: %+v", r)
	}
	if r := h(t, b, "input-events", touch(0, "up", 0.36, 0.36)); lines(r) != `{"c":false,"d":0,"i":0,"p":false,"t":"st","x":0.36,"y":0.36}` {
		t.Fatalf("up right after a move: %+v", r)
	}
}

// ReleaseSlot lifts only that slot's touches, as canceled touches at their
// last ACCEPTED position, in id order, before the slot's release line.
func TestReleaseSlotLiftsOnlyThatSlotsTouches(t *testing.T) {
	tr := NewTranslator()
	c := &fakeClock{t: time.Unix(1, 0)}
	tr.now = c.now
	x := func(v float64) *float64 { return &v }
	tr.Touch(Browser{Ph: "down", ID: 2, X: x(0.2), Y: x(0.3)}, 0)
	tr.Touch(Browser{Ph: "down", ID: 0, X: x(0.1), Y: x(0.1)}, 0)
	tr.Touch(Browser{Ph: "down", ID: 0, X: x(0.9), Y: x(0.9)}, 1)
	c.step(5 * time.Millisecond)
	tr.Touch(Browser{Ph: "move", ID: 2, X: x(0.25), Y: x(0.35)}, 0)
	tr.Touch(Browser{Ph: "move", ID: 2, X: x(0.5), Y: x(0.5)}, 0) // rate-dropped: not the last position
	got := fmt.Sprint(tr.ReleaseSlot(0))
	want := `[map[c:true d:0 i:0 p:false t:st x:0.1 y:0.1] map[c:true d:0 i:2 p:false t:st x:0.25 y:0.35] map[d:0 t:release]]`
	if got != want {
		t.Fatalf("ReleaseSlot(0):\n got %s\nwant %s", got, want)
	}
	if got := fmt.Sprint(tr.ReleaseSlot(0)); got != `[map[d:0 t:release]]` {
		t.Fatalf("second ReleaseSlot(0) lifted again: %s", got)
	}
	// Slot 1's touch survived slot 0's release; ReleaseAll lifts it.
	if got := fmt.Sprint(tr.ReleaseAll()); got != `[map[c:true d:1 i:10 p:false t:st x:0.9 y:0.9] map[t:release_all]]` {
		t.Fatalf("ReleaseAll: %s", got)
	}
}

// A browser release_all lifts the peer's touches on each of its slots.
func TestBrowserReleaseAllLiftsTouches(t *testing.T) {
	b := NewBinding()
	b.SetSlots(map[int]int{0: 1})
	clocked(b)
	h(t, b, "input-events", touch(5, "down", 0.5, 0.5))
	if r := h(t, b, "input-events", `{"t":"release_all"}`); lines(r) != `{"c":true,"d":1,"i":15,"p":false,"t":"st","x":0.5,"y":0.5} {"d":1,"t":"release"}` {
		t.Fatalf("release_all: %q", lines(r))
	}
}
