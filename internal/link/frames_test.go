package link

import (
	"bytes"
	"testing"

	"github.com/Startup-Suite/play-host/internal/probe"
)

func paint(seq uint16, w, h int) []byte {
	pix := make([]byte, w*h*4)
	bits := probe.Encode(seq)
	for i, on := range bits {
		if !on {
			continue
		}
		cx, cy := (i%probe.Cells)*probe.CellPx, (i/probe.Cells)*probe.CellPx
		for y := cy; y < cy+probe.CellPx; y++ {
			for x := cx; x < cx+probe.CellPx; x++ {
				o := (y*w + x) * 4
				pix[o], pix[o+1], pix[o+2], pix[o+3] = 255, 255, 255, 255
			}
		}
	}
	return pix
}

func TestDecodeCorner(t *testing.T) {
	for _, seq := range []uint16{1, 77, 4095} {
		got, err := DecodeCornerRGBA(paint(seq, 128, 80), 128)
		if err != nil || got != seq {
			t.Fatalf("seq %d: got %d %v", seq, got, err)
		}
	}
	if _, err := DecodeCornerRGBA(make([]byte, 128*80*4), 128); err != probe.ErrNoCode {
		t.Fatalf("black corner: %v", err)
	}
	if _, err := DecodeCornerRGBA(make([]byte, 10), 2); err != ErrShortFrame {
		t.Fatal("short frame accepted")
	}
}

func TestFrameHeaderRoundTrip(t *testing.T) {
	var b bytes.Buffer
	in := FrameHeader{Width: 1280, Height: 720, Kind: KindImageRGBA8, Length: 1280 * 720 * 4}
	if err := WriteFrameHeader(&b, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFrameHeader(&b)
	if err != nil || out != in {
		t.Fatalf("%+v %v", out, err)
	}
	b.Reset()
	_ = WriteFrameHeader(&b, FrameHeader{Width: 1280, Height: 720, Kind: 1, Length: 12})
	if _, err := ReadFrameHeader(&b); err == nil {
		t.Fatal("short length accepted")
	}
	if _, err := ReadFrameHeader(bytes.NewReader([]byte("XXXX0000000000000000"))); err == nil {
		t.Fatal("bad magic accepted")
	}
}
