package probe

import "testing"

func TestRoundTrip(t *testing.T) {
	for seq := uint16(1); seq <= MaxSeq; seq++ {
		got, err := Decode(Encode(seq))
		if err != nil || got != seq {
			t.Fatalf("seq %d: got %d err %v", seq, got, err)
		}
	}
}

func TestIdleCornerNeverDecodes(t *testing.T) {
	var black [Cells * Cells]bool
	if _, err := Decode(black); err != ErrNoCode {
		t.Fatalf("all-black corner decoded: %v", err)
	}
	var white [Cells * Cells]bool
	for i := range white {
		white[i] = true
	}
	if _, err := Decode(white); err != ErrNoCode {
		t.Fatalf("all-white corner decoded: %v", err)
	}
}

func TestSingleBitFlipIsRejectedOrDifferent(t *testing.T) {
	// A single wrong cell must never decode to the SAME seq with a valid check.
	bad := 0
	for seq := uint16(1); seq <= MaxSeq; seq += 7 {
		b := Encode(seq)
		for i := range b {
			f := b
			f[i] = !f[i]
			if got, err := Decode(f); err == nil && got == seq {
				t.Fatalf("flip %d of seq %d still decodes as %d", i, seq, got)
			} else if err == nil {
				bad++
			}
		}
	}
	// Flips in the check nibble always fail; flips in seq bits usually fail.
	t.Logf("single flips that alias to another valid seq: %d", bad)
}

// Wire values the GDScript and JS copies must reproduce.
func TestPinnedWireValues(t *testing.T) {
	cases := map[uint16]uint16{1: 0xB, 2: 0x8, 0x123: 0xA}
	for seq, want := range cases {
		if got := Check(seq); got != want {
			t.Errorf("Check(%#x) = %#x, want %#x", seq, got, want)
		}
	}
}

func TestSummarize(t *testing.T) {
	s := Summarize([]float64{5, 1, 4, 2, 3, 10, 9, 8, 7, 6})
	if s.N != 10 || s.P50 != 5 || s.P95 != 10 || s.Min != 1 || s.Max != 10 {
		t.Fatalf("got %+v", s)
	}
	if (Summarize(nil) != Summary{}) {
		t.Fatal("empty sample must be zero")
	}
}
