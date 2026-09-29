// Package input turns the browser's messages into the low-level lines the
// suite_play addon feeds to Input.parse_input_event. All mapping and
// diffing lives here, in Go, so it is unit-tested; the addon only resolves
// a Godot key name to a keycode and builds the InputEvent.
//
// Browser -> host, on the two data channels:
//
//	input-events (reliable, ordered):
//	  {"t":"key","code":"KeyW","down":true,"repeat":false,"src":0}
//	  {"t":"release_all"}
//	  {"t":"probe","seq":N,"src":0}
//	input-state (unordered, maxRetransmits 0; each message is a full,
//	idempotent snapshot, so a lost one is corrected by the next):
//	  {"t":"pad","src":0,"seq":N,"connected":true,
//	   "buttons":[17 numbers 0..1],"axes":[4 numbers -1..1]}   (W3C standard mapping)
//
// Host -> addon, newline-delimited JSON on the 127.0.0.1 link:
//
//	{"t":"key","d":0,"k":"W","loc":0,"p":true,"e":false}   d: slot; k: Godot key name; loc 0 none, 1 left, 2 right
//	{"t":"jb","d":0,"b":0,"p":true,"v":1}                 InputEventJoypadButton
//	{"t":"ja","d":0,"a":0,"v":-0.5}                      InputEventJoypadMotion
//	{"t":"release","d":0}   releases what device d holds (multi-peer)
//	{"t":"release_all"}  {"t":"probe","seq":N}  {"t":"export","on":true}
//
// The addon puts slot d's keys on InputEvent device <default key device> + d
// (16 + d in Godot 4.7, so P1's keys keep the device the built-in ui_*
// actions are bound to) and its pad events on device d; see suite_play.gd.
//
// SLOTS ARE HOST-HELD (task 01a0dbd6). `src` is the browser's local
// controller ordinal (0 = keyboard and the first pad; absent means 0). The
// player slot, which becomes the Godot device index `d`, comes ONLY from the
// table core sends in play_slots for (peer_id, src): see Binding. A `slot`
// key in a browser message is not even decoded, so a browser cannot name a
// slot. A peer whose table holds no slot for the message's src has the
// message dropped and counted (a spectator holds none at all).
package input

import (
	"encoding/json"
	"fmt"
	"math"
)

// Out is one line for the addon.
type Out map[string]any

// Browser is a decoded browser message.
type Browser struct {
	T         string    `json:"t"`
	Code      string    `json:"code,omitempty"`
	Down      bool      `json:"down,omitempty"`
	Repeat    bool      `json:"repeat,omitempty"`
	Src       int       `json:"src,omitempty"`
	Seq       int64     `json:"seq,omitempty"`
	Connected *bool     `json:"connected,omitempty"`
	Buttons   []float64 `json:"buttons,omitempty"`
	Axes      []float64 `json:"axes,omitempty"`
}

// Key locations, as Godot's KeyLocation enum.
const (
	LocNone  = 0
	LocLeft  = 1
	LocRight = 2
)

type keyName struct {
	name string
	loc  int
}

// keyMap is KeyboardEvent.code -> Godot key name (as understood by
// OS.find_keycode_from_string) and location. Physical positions on a US
// layout; the addon sets both keycode and physical_keycode from it.
var keyMap = func() map[string]keyName {
	m := map[string]keyName{
		"Escape": {"Escape", 0}, "Tab": {"Tab", 0}, "Backspace": {"Backspace", 0}, "Enter": {"Enter", 0},
		"Space": {"Space", 0}, "CapsLock": {"CapsLock", 0}, "NumLock": {"NumLock", 0}, "ScrollLock": {"ScrollLock", 0},
		"Pause": {"Pause", 0}, "PrintScreen": {"Print", 0}, "ContextMenu": {"Menu", 0},
		"Insert": {"Insert", 0}, "Delete": {"Delete", 0}, "Home": {"Home", 0}, "End": {"End", 0},
		"PageUp": {"PageUp", 0}, "PageDown": {"PageDown", 0},
		"ArrowLeft": {"Left", 0}, "ArrowUp": {"Up", 0}, "ArrowRight": {"Right", 0}, "ArrowDown": {"Down", 0},
		"ShiftLeft": {"Shift", LocLeft}, "ShiftRight": {"Shift", LocRight},
		"ControlLeft": {"Ctrl", LocLeft}, "ControlRight": {"Ctrl", LocRight},
		"AltLeft": {"Alt", LocLeft}, "AltRight": {"Alt", LocRight},
		"MetaLeft": {"Meta", LocLeft}, "MetaRight": {"Meta", LocRight},
		"Minus": {"Minus", 0}, "Equal": {"Equal", 0}, "BracketLeft": {"BracketLeft", 0}, "BracketRight": {"BracketRight", 0},
		"Backslash": {"BackSlash", 0}, "Semicolon": {"Semicolon", 0}, "Quote": {"Apostrophe", 0}, "Backquote": {"QuoteLeft", 0},
		"Comma": {"Comma", 0}, "Period": {"Period", 0}, "Slash": {"Slash", 0},
		"NumpadAdd": {"Kp Add", 0}, "NumpadSubtract": {"Kp Subtract", 0}, "NumpadMultiply": {"Kp Multiply", 0},
		"NumpadDivide": {"Kp Divide", 0}, "NumpadDecimal": {"Kp Period", 0}, "NumpadEnter": {"Kp Enter", 0},
	}
	for c := 'A'; c <= 'Z'; c++ {
		m["Key"+string(c)] = keyName{string(c), 0}
	}
	for d := '0'; d <= '9'; d++ {
		m["Digit"+string(d)] = keyName{string(d), 0}
		m["Numpad"+string(d)] = keyName{"Kp " + string(d), 0}
	}
	for f := 1; f <= 12; f++ {
		m[fmt.Sprintf("F%d", f)] = keyName{fmt.Sprintf("F%d", f), 0}
	}
	return m
}()

// KeyNames lists every Godot key name the map can produce (for the addon's
// resolve check on the host).
func KeyNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range keyMap {
		if !seen[k.name] {
			seen[k.name] = true
			out = append(out, k.name)
		}
	}
	return out
}

// W3C standard gamepad mapping -> Godot JoyButton, or an axis for triggers.
const (
	joyA, joyB, joyX, joyY               = 0, 1, 2, 3
	joyBack, joyGuide, joyStart          = 4, 5, 6
	joyLeftStick, joyRightStick          = 7, 8
	joyLeftShoulder, joyRightShoulder    = 9, 10
	joyDpadUp, joyDpadDown               = 11, 12
	joyDpadLeft, joyDpadRight            = 13, 14
	axisLeftX, axisLeftY, axisRX, axisRY = 0, 1, 2, 3
	axisTriggerLeft, axisTriggerRight    = 4, 5
	stdButtons, stdAxes                  = 17, 4
	pressThreshold                       = 0.5
	axisEpsilon                          = 1.0 / 512
)

// buttonMap[i] is the Godot JoyButton for standard button i, or -1 when the
// standard button is an analog trigger sent as a Godot axis (triggerAxis).
var buttonMap = [stdButtons]int{
	joyA, joyB, joyX, joyY, joyLeftShoulder, joyRightShoulder, -1, -1,
	joyBack, joyStart, joyLeftStick, joyRightStick, joyDpadUp, joyDpadDown, joyDpadLeft, joyDpadRight, joyGuide,
}
var triggerAxis = map[int]int{6: axisTriggerLeft, 7: axisTriggerRight}
var axisMap = [stdAxes]int{axisLeftX, axisLeftY, axisRX, axisRY}

type pad struct {
	seq     int64
	have    bool
	pressed [stdButtons]bool
	trig    [2]float64
	axes    [stdAxes]float64
}

// Translator holds the diff state for one viewer connection.
type Translator struct {
	keys map[int]map[string]bool // slot -> held codes
	pads map[int]*pad
}

// NewTranslator starts with nothing held.
func NewTranslator() *Translator {
	return &Translator{keys: map[int]map[string]bool{}, pads: map[int]*pad{}}
}

// Result is what one browser message turned into.
type Result struct {
	Lines []Out
	// Activity is true when the message changed game input (key edge, pad
	// change, release, probe). A pad snapshot that changes nothing is NOT
	// activity, so a controller left plugged in cannot hold a session open
	// past its idle timeout.
	Activity bool
	// Slot is the slot the message was bound to, or -1 (release_all, a
	// dropped message).
	Slot int
	// Dropped names why the message was dropped ("" when it was not):
	// DropNoSlot for a src with no slot in the table.
	Dropped string
}

// Drop reasons.
const (
	DropNoSlot       = "no slot"
	DropProbeNoSpace = "probe slot outside the 2-bit namespace"
)

// Decode parses one data-channel message.
func Decode(label string, data []byte) (Browser, error) {
	var m Browser
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%s: %w", label, err)
	}
	if m.Src < 0 {
		m.Src = 0
	}
	return m, nil
}

// Key translates a key edge for slot.
func (t *Translator) Key(m Browser, slot int) Result {
	k, ok := keyMap[m.Code]
	if !ok {
		return Result{Slot: slot}
	}
	held := t.keys[slot]
	if held == nil {
		held = map[string]bool{}
		t.keys[slot] = held
	}
	if m.Down {
		if held[m.Code] && !m.Repeat {
			return Result{Slot: slot} // duplicate down, not an edge
		}
		held[m.Code] = true
	} else {
		if !held[m.Code] {
			return Result{Slot: slot} // up without down (focus came in mid-press)
		}
		delete(held, m.Code)
	}
	return Result{Lines: []Out{{"t": "key", "d": slot, "k": k.name, "loc": k.loc, "p": m.Down, "e": m.Down && m.Repeat}}, Activity: true, Slot: slot}
}

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(lo, math.Min(hi, v))
}

// Pad diffs a pad snapshot for slot.
func (t *Translator) Pad(m Browser, slot int) Result {
	p := t.pads[slot]
	if p == nil {
		p = &pad{}
		t.pads[slot] = p
	}
	if p.have && m.Seq <= p.seq {
		return Result{Slot: slot} // stale or duplicate snapshot on the unordered channel
	}
	p.have, p.seq = true, m.Seq
	if m.Connected != nil && !*m.Connected {
		return Result{Lines: t.releasePad(slot), Activity: true, Slot: slot}
	}
	var out []Out
	for i := 0; i < stdButtons; i++ {
		v := 0.0
		if i < len(m.Buttons) {
			v = clamp(m.Buttons[i], 0, 1)
		}
		if a, isTrig := triggerAxis[i]; isTrig {
			ti := a - axisTriggerLeft
			if math.Abs(v-p.trig[ti]) >= axisEpsilon || (v == 0 && p.trig[ti] != 0) {
				p.trig[ti] = v
				out = append(out, Out{"t": "ja", "d": slot, "a": a, "v": v})
			}
			continue
		}
		pressed := v >= pressThreshold
		if pressed != p.pressed[i] {
			p.pressed[i] = pressed
			out = append(out, Out{"t": "jb", "d": slot, "b": buttonMap[i], "p": pressed, "v": v})
		}
	}
	for i := 0; i < stdAxes; i++ {
		v := 0.0
		if i < len(m.Axes) {
			v = clamp(m.Axes[i], -1, 1)
		}
		if math.Abs(v-p.axes[i]) >= axisEpsilon || (v == 0 && p.axes[i] != 0) {
			p.axes[i] = v
			out = append(out, Out{"t": "ja", "d": slot, "a": axisMap[i], "v": v})
		}
	}
	return Result{Lines: out, Activity: len(out) > 0, Slot: slot}
}

func (t *Translator) releasePad(slot int) []Out {
	p := t.pads[slot]
	if p == nil {
		return nil
	}
	var out []Out
	for i, on := range p.pressed {
		if on {
			out = append(out, Out{"t": "jb", "d": slot, "b": buttonMap[i], "p": false, "v": 0.0})
			p.pressed[i] = false
		}
	}
	for i, v := range p.trig {
		if v != 0 {
			out = append(out, Out{"t": "ja", "d": slot, "a": axisTriggerLeft + i, "v": 0.0})
			p.trig[i] = 0
		}
	}
	for i, v := range p.axes {
		if v != 0 {
			out = append(out, Out{"t": "ja", "d": slot, "a": axisMap[i], "v": 0.0})
			p.axes[i] = 0
		}
	}
	return out
}

func (t *Translator) releaseKeys(slot int) []Out {
	var out []Out
	for code := range t.keys[slot] {
		k := keyMap[code]
		out = append(out, Out{"t": "key", "d": slot, "k": k.name, "loc": k.loc, "p": false, "e": false})
	}
	delete(t.keys, slot)
	return out
}

// ReleaseAll releases every held key and pad input on every slot, then asks
// the addon to release whatever it believes is held (belt and braces: the
// addon also tracks what it pressed). This is the v1 (single-peer) release:
// the addon's release_all drops EVERY device, so a multi-peer session uses
// ReleaseSlot per slot instead.
func (t *Translator) ReleaseAll() []Out {
	var out []Out
	for slot := range t.keys {
		out = append(out, t.releaseKeys(slot)...)
	}
	for slot := range t.pads {
		out = append(out, t.releasePad(slot)...)
	}
	return append(out, Out{"t": "release_all"})
}

// ReleaseSlot releases what this translator holds on one slot, then asks the
// addon to release whatever it believes device `slot` holds. The pad's diff
// state is forgotten, so the next snapshot for the slot starts clean.
func (t *Translator) ReleaseSlot(slot int) []Out {
	out := t.releaseKeys(slot)
	out = append(out, t.releasePad(slot)...)
	delete(t.pads, slot)
	return append(out, Out{"t": "release", "d": slot})
}
