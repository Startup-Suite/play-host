package input

import (
	"sort"
	"sync"
)

// ProbeSlotBits is how many of a probe's 12 seq bits carry the slot in a
// multi-peer session (task 01a0dbd6): the host forwards
// slot<<ProbeSeqBits | seq&ProbeSeqMask, so N players' probes are never
// confused in the one corner code. Slots 0-3 fit; a probe from a higher
// slot is dropped (DropProbeNoSpace) rather than aliased onto slot 0-3.
const (
	ProbeSlotBits = 2
	ProbeSeqBits  = 10
	ProbeSeqMask  = 1<<ProbeSeqBits - 1
	ProbeMaxSlot  = 1<<ProbeSlotBits - 1
)

// ProbeCode is the seq the addon draws for a probe from slot.
func ProbeCode(slot int, seq int64) int64 {
	return int64(slot)<<ProbeSeqBits | seq&ProbeSeqMask
}

// Binding is one peer's input: its translator plus the slot table core sent
// for it. It is the ONLY place a browser message acquires a slot, and it
// takes the slot from the table, never from the message.
//
// Two modes:
//   - v1 (NewV1Binding): an old core that sends no play_slots. src 0 is slot
//     0, release_all is the addon's global release_all, and a probe's seq is
//     forwarded unchanged, exactly as before task 01a0dbd6.
//   - multi (NewBinding): the table starts EMPTY, so a peer is a spectator
//     until play_slots names a slot for it.
//
// Safe for concurrent use: pion delivers the two data channels on their own
// goroutines while the session loop applies play_slots.
type Binding struct {
	mu    sync.Mutex
	tr    *Translator
	slots map[int]int // src -> slot
	v1    bool
}

// NewBinding is a multi-peer binding with no slots (a spectator).
func NewBinding() *Binding {
	return &Binding{tr: NewTranslator(), slots: map[int]int{}}
}

// NewV1Binding is the single implicit peer of an old core: src 0 is slot 0.
func NewV1Binding() *Binding {
	return &Binding{tr: NewTranslator(), slots: map[int]int{0: 0}, v1: true}
}

// Slots is a copy of the src -> slot table.
func (b *Binding) Slots() map[int]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[int]int, len(b.slots))
	for k, v := range b.slots {
		out[k] = v
	}
	return out
}

// SetSlots replaces the table with core's snapshot for this peer and returns
// the release lines for every slot the peer no longer holds (a leave, a
// take-over), so nothing stays pressed on a slot someone else now drives.
// A slot that moved to a different src is released too: its held state
// belonged to the old controller.
func (b *Binding) SetSlots(table map[int]int) []Out {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.v1 {
		return nil
	}
	next := make(map[int]int, len(table))
	for src, slot := range table {
		if src >= 0 && slot >= 0 {
			next[src] = slot
		}
	}
	var lost []int
	for src, slot := range b.slots {
		if s, ok := next[src]; !ok || s != slot {
			lost = append(lost, slot)
		}
	}
	b.slots = next
	sort.Ints(lost)
	var out []Out
	for _, slot := range lost {
		out = append(out, b.tr.ReleaseSlot(slot)...)
	}
	return out
}

// Release releases everything this peer holds (the peer is closing or being
// replaced). v1 keeps the global release_all line.
func (b *Binding) Release() []Out {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.v1 {
		return b.tr.ReleaseAll()
	}
	var out []Out
	for _, slot := range sortedSlots(b.slots) {
		out = append(out, b.tr.ReleaseSlot(slot)...)
	}
	return out
}

// Handle decodes and translates one data-channel message from this peer.
// An error is a message that did not parse; a dropped message is a Result
// with Dropped set.
func (b *Binding) Handle(label string, data []byte) (Result, error) {
	m, err := Decode(label, data)
	if err != nil {
		return Result{Slot: -1}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch m.T {
	case "key", "pad", "probe", "touch":
	case "release_all":
		// Everything this peer holds, and nothing anyone else holds.
		if len(b.slots) == 0 {
			return Result{Slot: -1, Dropped: DropNoSlot}, nil
		}
		if b.v1 {
			return Result{Lines: b.tr.ReleaseAll(), Activity: true, Slot: -1}, nil
		}
		var out []Out
		for _, slot := range sortedSlots(b.slots) {
			out = append(out, b.tr.ReleaseSlot(slot)...)
		}
		return Result{Lines: out, Activity: true, Slot: -1}, nil
	default:
		// Unknown types are ignored (forward compatibility), and are not
		// counted as drops.
		return Result{Slot: -1}, nil
	}
	slot, ok := b.slots[m.Src]
	if !ok {
		return Result{Slot: -1, Dropped: DropNoSlot}, nil
	}
	switch m.T {
	case "key":
		return b.tr.Key(m, slot), nil
	case "pad":
		return b.tr.Pad(m, slot), nil
	case "touch":
		return b.tr.Touch(m, slot), nil
	default: // probe
		seq := m.Seq
		if !b.v1 {
			if slot > ProbeMaxSlot {
				return Result{Slot: slot, Dropped: DropProbeNoSpace}, nil
			}
			seq = ProbeCode(slot, seq)
		}
		return Result{Lines: []Out{{"t": "probe", "seq": seq}}, Activity: true, Slot: slot}, nil
	}
}

func sortedSlots(m map[int]int) []int {
	seen := map[int]bool{}
	var out []int
	for _, slot := range m {
		if !seen[slot] {
			seen[slot] = true
			out = append(out, slot)
		}
	}
	sort.Ints(out)
	return out
}
