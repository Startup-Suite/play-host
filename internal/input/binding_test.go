package input

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func lines(r Result) string {
	var s []string
	for _, l := range r.Lines {
		s = append(s, js(l))
	}
	return strings.Join(s, " ")
}

// The forged-input table (task 01a0dbd6 stage 3). Input goes browser to host
// over data channels and never through core, so this is the only place a
// spectator's message can be stopped. Every row is a message a hostile page
// can send; "want" is the addon lines it may produce.
func TestForgedInputTable(t *testing.T) {
	spectator := NewBinding() // attached, holds no slot
	player := NewBinding()
	player.SetSlots(map[int]int{0: 1}) // this peer is P2
	b := make([]float64, 17)
	b[0] = 1

	rows := []struct {
		name string
		peer *Binding
		msg  string
		want string // exact lines; "" = none
		drop string
	}{
		{"spectator key", spectator, `{"t":"key","code":"KeyD","down":true}`, "", DropNoSlot},
		{"spectator key naming slot 0", spectator, `{"t":"key","code":"KeyD","down":true,"slot":0}`, "", DropNoSlot},
		{"spectator pad", spectator, `{"t":"pad","slot":1,"src":0,"seq":999,"connected":true,"buttons":[1],"axes":[1,0,0,0]}`, "", DropNoSlot},
		{"spectator probe", spectator, `{"t":"probe","seq":5}`, "", DropNoSlot},
		{"spectator release_all", spectator, `{"t":"release_all"}`, "", DropNoSlot},
		{"spectator src 3", spectator, `{"t":"key","code":"KeyW","down":true,"src":3}`, "", DropNoSlot},
		{"player naming another's slot", player, `{"t":"key","code":"KeyD","down":true,"slot":0}`, `{"d":1,"e":false,"k":"D","loc":0,"p":true,"t":"key"}`, ""},
		{"player pad naming slot 0", player, `{"t":"pad","slot":0,"seq":1,"connected":true,"buttons":[1],"axes":[0,0,0,0]}`, `{"b":0,"d":1,"p":true,"t":"jb","v":1}`, ""},
		{"player src it does not hold", player, `{"t":"key","code":"KeyW","down":true,"src":1}`, "", DropNoSlot},
		{"player probe is namespaced to its slot", player, `{"t":"probe","seq":5,"slot":0}`, `{"seq":1029,"t":"probe"}`, ""},
	}
	for _, r := range rows {
		got, err := r.peer.Handle("input-events", []byte(r.msg))
		if err != nil {
			t.Fatalf("%s: %v", r.name, err)
		}
		if lines(got) != r.want || got.Dropped != r.drop {
			t.Errorf("%s:\n got lines %q dropped %q\nwant lines %q dropped %q", r.name, lines(got), got.Dropped, r.want, r.drop)
		}
		if r.drop != "" && got.Activity {
			t.Errorf("%s: a dropped message counted as activity (it would hold the session open)", r.name)
		}
	}
}

// Probe namespacing: slot<<10 | seq&0x3FF, so four players' probes are
// distinct in the 12-bit corner code; a slot past 3 cannot be encoded and is
// dropped instead of aliasing onto P1-P4.
func TestProbeNamespacing(t *testing.T) {
	for _, c := range []struct {
		slot int
		seq  int64
		want int64
	}{
		{0, 5, 5}, {1, 5, 1029}, {2, 5, 2053}, {3, 5, 3077}, {3, 1023, 4095}, {1, 1024 + 7, 1024 + 7}, {0, 4095, 1023},
	} {
		if got := ProbeCode(c.slot, c.seq); got != c.want {
			t.Errorf("ProbeCode(%d, %d) = %d, want %d", c.slot, c.seq, got, c.want)
		}
		if got := ProbeCode(c.slot, c.seq) >> ProbeSeqBits; got != int64(c.slot) {
			t.Errorf("slot %d does not round-trip from the top bits: %d", c.slot, got)
		}
	}
	p := NewBinding()
	p.SetSlots(map[int]int{0: 4})
	r, _ := p.Handle("input-events", []byte(`{"t":"probe","seq":5}`))
	if r.Dropped != DropProbeNoSpace || len(r.Lines) != 0 {
		t.Fatalf("slot 4 probe: %+v", r)
	}
	r, _ = p.Handle("input-events", []byte(`{"t":"key","code":"KeyW","down":true}`))
	if r.Dropped != "" || r.Lines[0]["d"] != 4 {
		t.Fatalf("slot 4 keys still work: %+v", r)
	}
}

// release_all from one peer releases only that peer's slots, per slot, and
// never the addon's global release_all (which drops every device).
func TestReleasePerPeer(t *testing.T) {
	a, b := NewBinding(), NewBinding()
	a.SetSlots(map[int]int{0: 0, 1: 2}) // one browser, two pads: P1 and P3
	b.SetSlots(map[int]int{0: 1})
	a.Handle("input-events", []byte(`{"t":"key","code":"KeyW","down":true}`))
	b.Handle("input-events", []byte(`{"t":"key","code":"KeyS","down":true}`))
	btn := `{"t":"pad","src":1,"seq":1,"connected":true,"buttons":[0,1],"axes":[0,0,0,0]}`
	a.Handle("input-state", []byte(btn))

	r, _ := a.Handle("input-events", []byte(`{"t":"release_all"}`))
	want := `{"d":0,"e":false,"k":"W","loc":0,"p":false,"t":"key"} {"d":0,"t":"release"} {"b":1,"d":2,"p":false,"t":"jb","v":0} {"d":2,"t":"release"}`
	if lines(r) != want {
		t.Fatalf("a's release_all:\n got %s\nwant %s", lines(r), want)
	}
	if strings.Contains(lines(r), `"release_all"`) || strings.Contains(lines(r), `"d":1`) {
		t.Fatalf("a's release touched b: %s", lines(r))
	}
	// b's key is still held: its up is an edge.
	r, _ = b.Handle("input-events", []byte(`{"t":"key","code":"KeyS","down":false}`))
	if lines(r) != `{"d":1,"e":false,"k":"S","loc":0,"p":false,"t":"key"}` {
		t.Fatalf("b's key was released by a: %s", lines(r))
	}
}

// A play_slots snapshot that takes a slot away (leave, take-over) releases
// what the peer held there, so nothing stays pressed for the new holder.
func TestSetSlotsReleasesLostSlots(t *testing.T) {
	p := NewBinding()
	p.SetSlots(map[int]int{0: 0})
	p.Handle("input-events", []byte(`{"t":"key","code":"KeyD","down":true}`))
	got, _, _ := p.SetSlots(map[int]int{})
	var s []string
	for _, l := range got {
		s = append(s, js(l))
	}
	want := []string{`{"d":0,"e":false,"k":"D","loc":0,"p":false,"t":"key"}`, `{"d":0,"t":"release"}`}
	if fmt.Sprint(s) != fmt.Sprint(want) {
		t.Fatalf("lost slot release:\n got %v\nwant %v", s, want)
	}
	if r, _ := p.Handle("input-events", []byte(`{"t":"key","code":"KeyD","down":true}`)); r.Dropped != DropNoSlot {
		t.Fatalf("a displaced peer still drives the slot: %+v", r)
	}
	// Same table again: nothing to release (idempotent snapshot).
	p.SetSlots(map[int]int{0: 3})
	if got, _, _ := p.SetSlots(map[int]int{0: 3}); len(got) != 0 {
		t.Fatalf("an unchanged snapshot released %v", got)
	}
}

// A spectator's touch (task 01a0fe45) is dropped as DropNoSlot whatever it
// carries, and a player's touch binds to its table slot, not to any `slot`
// it names.
func TestForgedTouch(t *testing.T) {
	spectator := NewBinding()
	clocked(spectator)
	for _, m := range []string{
		touch(0, "down", 0.5, 0.5),
		`{"t":"touch","src":0,"id":0,"ph":"down","x":0.5,"y":0.5,"slot":0}`,
		`{"t":"touch","src":0,"id":99,"ph":"bogus"}`,
	} {
		if r := h(t, spectator, "input-events", m); r.Dropped != DropNoSlot || len(r.Lines) != 0 || r.Activity {
			t.Errorf("spectator %s: %+v", m, r)
		}
	}
	player := NewBinding()
	player.SetSlots(map[int]int{0: 1})
	clocked(player)
	if r := h(t, player, "input-events", `{"t":"touch","src":0,"id":0,"ph":"down","x":0.5,"y":0.5,"slot":0}`); lines(r) != `{"c":false,"d":1,"i":10,"p":true,"t":"st","x":0.5,"y":0.5}` {
		t.Fatalf("player touch naming slot 0: %q", lines(r))
	}
}

// SetSlots losing a slot (a take-over or a leave) lifts that slot's touches
// and nothing on the slot the peer keeps; Release (peer close) lifts the rest.
func TestSetSlotsLossReleasesThatSlotsTouches(t *testing.T) {
	b := NewBinding()
	b.SetSlots(map[int]int{0: 0, 1: 1})
	b.tr.now = (&fakeClock{t: time.Unix(1, 0)}).now
	x := func(v float64) *float64 { return &v }
	// src 0 only sends touch, so put slot 1's touch on the translator.
	h(t, b, "input-events", touch(0, "down", 0.1, 0.2))
	b.tr.Touch(Browser{Ph: "down", ID: 0, X: x(0.7), Y: x(0.8)}, 1)
	lost, _, _ := b.SetSlots(map[int]int{1: 1})
	got := fmt.Sprint(lost)
	if got != `[map[c:true d:0 i:0 p:false t:st x:0.1 y:0.2] map[d:0 t:release]]` {
		t.Fatalf("losing slot 0:\n%s", got)
	}
	if got := fmt.Sprint(b.Release()); got != `[map[c:true d:1 i:10 p:false t:st x:0.7 y:0.8] map[d:1 t:release]]` {
		t.Fatalf("Release:\n%s", got)
	}
}

// SetSlots reports what it changed (task 01a0ff61): the host logs a bind or
// an unbind only when one happened, so an unchanged play_slots (sent for
// every peer on every change) logs nothing.
func TestSetSlotsReportsChanges(t *testing.T) {
	b := NewBinding()
	steps := []struct {
		table          map[int]int
		bound, unbound string
	}{
		{map[int]int{0: 1}, "[{0 1}]", "[]"},
		{map[int]int{0: 1}, "[]", "[]"}, // unchanged: nothing
		{map[int]int{2: 3, 0: 1, 1: 0}, "[{1 0} {2 3}]", "[]"},
		{map[int]int{0: 2, 1: 0, 2: 3}, "[{0 2}]", "[{0 1}]"}, // src 0 moved slot: unbind + bind
		{map[int]int{1: 0, 3: 2}, "[{3 2}]", "[{0 2} {2 3}]"}, // slot 2 moved src: unbind + bind
		{map[int]int{1: 0, -1: 4, 5: -2}, "[]", "[{3 2}]"},    // negative entries ignored
		{map[int]int{}, "[]", "[{1 0}]"},                      // spectator
		{nil, "[]", "[]"},                                     // still a spectator
	}
	for i, st := range steps {
		_, bound, unbound := b.SetSlots(st.table)
		if g := fmt.Sprint(bound); g != st.bound && !(st.bound == "[]" && len(bound) == 0) {
			t.Errorf("step %d %v: bound %s, want %s", i, st.table, g, st.bound)
		}
		if g := fmt.Sprint(unbound); g != st.unbound && !(st.unbound == "[]" && len(unbound) == 0) {
			t.Errorf("step %d %v: unbound %s, want %s", i, st.table, g, st.unbound)
		}
	}
	v1 := NewV1Binding()
	if lines, bound, unbound := v1.SetSlots(map[int]int{0: 3}); lines != nil || bound != nil || unbound != nil {
		t.Fatalf("v1 reported a change: %v %v %v", lines, bound, unbound)
	}
}
