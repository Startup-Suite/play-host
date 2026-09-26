package spike

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/Startup-Suite/play-host/internal/probe"
)

// Frame kinds written by addons/suite_play.
const (
	KindImageRGBA8 = 1 // get_viewport().get_texture().get_image()
	KindRDTexture  = 2 // RenderingDevice.texture_get_data_async
)

// FrameHeader precedes every exported frame on the addon link.
type FrameHeader struct {
	Width, Height, Kind, Length uint32
}

var magic = [4]byte{'S', 'P', 'F', '1'}

// ReadFrameHeader reads one "SPF1" header.
func ReadFrameHeader(r io.Reader) (FrameHeader, error) {
	var b [20]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return FrameHeader{}, err
	}
	if [4]byte(b[:4]) != magic {
		return FrameHeader{}, fmt.Errorf("bad frame magic %q", b[:4])
	}
	h := FrameHeader{
		Width:  binary.LittleEndian.Uint32(b[4:]),
		Height: binary.LittleEndian.Uint32(b[8:]),
		Kind:   binary.LittleEndian.Uint32(b[12:]),
		Length: binary.LittleEndian.Uint32(b[16:]),
	}
	if h.Width == 0 || h.Height == 0 || h.Width > 7680 || h.Height > 4320 {
		return h, fmt.Errorf("implausible frame size %dx%d", h.Width, h.Height)
	}
	if h.Length < h.Width*h.Height*4 || h.Length > h.Width*h.Height*16 {
		return h, fmt.Errorf("frame length %d does not fit %dx%d", h.Length, h.Width, h.Height)
	}
	return h, nil
}

// WriteFrameHeader is the Go mirror of the addon's writer (used by tests).
func WriteFrameHeader(w io.Writer, h FrameHeader) error {
	var b [20]byte
	copy(b[:4], magic[:])
	binary.LittleEndian.PutUint32(b[4:], h.Width)
	binary.LittleEndian.PutUint32(b[8:], h.Height)
	binary.LittleEndian.PutUint32(b[12:], h.Kind)
	binary.LittleEndian.PutUint32(b[16:], h.Length)
	_, err := w.Write(b[:])
	return err
}

// ErrShortFrame means the buffer cannot hold the probe corner.
var ErrShortFrame = errors.New("frame smaller than probe corner")

// DecodeCornerRGBA reads the probe code from a tightly packed RGBA8 frame by
// sampling each cell's centre pixel.
func DecodeCornerRGBA(pix []byte, width int) (uint16, error) {
	edge := probe.Cells * probe.CellPx
	if width < edge || len(pix) < width*4*edge {
		return 0, ErrShortFrame
	}
	var bits [probe.Cells * probe.Cells]bool
	for i := range bits {
		x := (i%probe.Cells)*probe.CellPx + probe.CellPx/2
		y := (i/probe.Cells)*probe.CellPx + probe.CellPx/2
		o := (y*width + x) * 4
		lum := (299*int(pix[o]) + 587*int(pix[o+1]) + 114*int(pix[o+2])) / 1000
		bits[i] = lum >= 128
	}
	return probe.Decode(bits)
}
