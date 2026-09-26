// Package input turns the browser's messages into the low-level lines the
// suite_play addon feeds to Input.parse_input_event. All mapping and
// diffing lives here, in Go, so it is unit-tested; the addon only resolves
// a Godot key name to a keycode and builds the InputEvent.
//
// Browser -> host, on the two data channels:
//
//	input-events (reliable, ordered):
//	  {"t":"key","code":"KeyW","down":true,"repeat":false,"slot":0}
//	  {"t":"release_all"}
//	  {"t":"probe","seq":N}
//	input-state (unordered, maxRetransmits 0; each message is a full,
//	idempotent snapshot, so a lost one is corrected by the next):
//	  {"t":"pad","slot":0,"seq":N,"connected":true,
//	   "buttons":[17 numbers 0..1],"axes":[4 numbers -1..1]}   (W3C standard mapping)
//
// Host -> addon, newline-delimited JSON on the 127.0.0.1 link:
//
//	{"t":"key","k":"W","loc":0,"p":true,"e":false}   k: Godot key name; loc 0 none, 1 left, 2 right
//	{"t":"jb","d":0,"b":0,"p":true,"v":1}             InputEventJoypadButton
//	{"t":"ja","d":0,"a":0,"v":-0.5}                  InputEventJoypadMotion
//	{"t":"release_all"}  {"t":"probe","seq":N}  {"t":"export","on":true}
//
// `slot` is the player slot and becomes the Godot device index. v1 plays
// one slot (0); the multi-player follow-on (task 01a0dbd6) needs no new
// message shape, only more than one slot allowed (MaxSlots).
package input

import (
	"encoding/json"
	"fmt"
	"math"
)

// MaxSlots is how many player slots the host accepts. v1 is single-player.
var MaxSlots = 1

// Out is one line for the addon.
type Out map[string]any

// Browser is a decoded browser message.
type Browser struct {
	T         string    `json:"t"`
	Code      string    `json:"code,omitempty"`
	Down      bool      `json:"down,omitempty"`
	Repeat    bool      `json:"repeat,omitempty"`
	Slot      int       `json:"slot,omitempty"`
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
	// change, release_all, probe). A pad snapshot that changes nothing is
	// NOT activity, so a controller left plugged in cannot hold a session
	// open past its idle timeout.
	Activity bool
}

// Handle decodes and translates one data-channel message.
func (t *Translator) Handle(label string, data []byte) (Result, error) {
	var m Browser
	if err := json.Unmarshal(data, &m); err != nil {
		return Result{}, fmt.Errorf("%s: %w", label, err)
	}
	if m.Slot < 0 || m.Slot >= MaxSlots {
		return Result{}, fmt.Errorf("slot %d out of range (max %d)", m.Slot, MaxSlots)
	}
	switch m.T {
	case "key":
		return t.key(m), nil
	case "pad":
		return t.pad(m), nil
	case "release_all":
		return Result{Lines: t.ReleaseAll(), Activity: true}, nil
	case "probe":
		return Result{Lines: []Out{{"t": "probe", "seq": m.Seq}}, Activity: true}, nil
	default:
		// Unknown types are ignored (forward compatibility).
		return Result{}, nil
	}
}

func (t *Translator) key(m Browser) Result {
	k, ok := keyMap[m.Code]
	if !ok {
		return Result{}
	}
	held := t.keys[m.Slot]
	if held == nil {
		held = map[string]bool{}
		t.keys[m.Slot] = held
	}
	if m.Down {
		if held[m.Code] && !m.Repeat {
			return Result{} // duplicate down, not an edge
		}
		held[m.Code] = true
	} else {
		if !held[m.Code] {
			return Result{} // up without down (focus came in mid-press)
		}
		delete(held, m.Code)
	}
	return Result{Lines: []Out{{"t": "key", "k": k.name, "loc": k.loc, "p": m.Down, "e": m.Down && m.Repeat}}, Activity: true}
}

func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(lo, math.Min(hi, v))
}

func (t *Translator) pad(m Browser) Result {
	p := t.pads[m.Slot]
	if p == nil {
		p = &pad{}
		t.pads[m.Slot] = p
	}
	if p.have && m.Seq <= p.seq {
		return Result{} // stale or duplicate snapshot on the unordered channel
	}
	p.have, p.seq = true, m.Seq
	if m.Connected != nil && !*m.Connected {
		return Result{Lines: t.releasePad(m.Slot), Activity: true}
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
				out = append(out, Out{"t": "ja", "d": m.Slot, "a": a, "v": v})
			}
			continue
		}
		pressed := v >= pressThreshold
		if pressed != p.pressed[i] {
			p.pressed[i] = pressed
			out = append(out, Out{"t": "jb", "d": m.Slot, "b": buttonMap[i], "p": pressed, "v": v})
		}
	}
	for i := 0; i < stdAxes; i++ {
		v := 0.0
		if i < len(m.Axes) {
			v = clamp(m.Axes[i], -1, 1)
		}
		if math.Abs(v-p.axes[i]) >= axisEpsilon || (v == 0 && p.axes[i] != 0) {
			p.axes[i] = v
			out = append(out, Out{"t": "ja", "d": m.Slot, "a": axisMap[i], "v": v})
		}
	}
	return Result{Lines: out, Activity: len(out) > 0}
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

// ReleaseAll releases every held key and pad input on every slot, then asks
// the addon to release whatever it believes is held (belt and braces: the
// addon also tracks what it pressed).
func (t *Translator) ReleaseAll() []Out {
	var out []Out
	for slot, held := range t.keys {
		for code := range held {
			k := keyMap[code]
			out = append(out, Out{"t": "key", "k": k.name, "loc": k.loc, "p": false, "e": false})
		}
		delete(t.keys, slot)
	}
	for slot := range t.pads {
		out = append(out, t.releasePad(slot)...)
	}
	return append(out, Out{"t": "release_all"})
}
