package media

import (
	"bytes"
	"testing"
)

func nal(typ byte, body ...byte) []byte {
	return append([]byte{0, 0, 0, 1, typ}, body...)
}

func TestAccessUnitsGroupsParameterSetsWithSlice(t *testing.T) {
	var s []byte
	s = append(s, nal(0x09, 0xF0)...)             // AUD
	s = append(s, nal(0x67, 0x42, 0xE0, 0x1F)...) // SPS
	s = append(s, nal(0x68, 0xCE)...)             // PPS
	s = append(s, nal(0x65, 0x88, 0x84)...)       // IDR slice
	s = append(s, nal(0x09, 0xF0)...)             // AUD
	s = append(s, nal(0x41, 0x9A, 0x01)...)       // non-IDR slice
	var aus [][]byte
	if err := AccessUnits(bytes.NewReader(s), func(au []byte) error {
		aus = append(aus, append([]byte(nil), au...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(aus) != 2 {
		t.Fatalf("want 2 access units, got %d", len(aus))
	}
	if n := bytes.Count(aus[0], []byte{0, 0, 0, 1}); n != 4 {
		t.Errorf("first AU holds %d NALs, want AUD+SPS+PPS+IDR", n)
	}
	if !bytes.HasSuffix(aus[1], []byte{0x41, 0x9A, 0x01}) {
		t.Errorf("second AU ends %x", aus[1])
	}
}
