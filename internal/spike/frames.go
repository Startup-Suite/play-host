package spike

import "github.com/Startup-Suite/play-host/internal/link"

// The frame record moved to internal/link in stage 3; these keep the
// stage-1 harness reading as it did.
type FrameHeader = link.FrameHeader

const (
	KindImageRGBA8 = link.KindImageRGBA8
	KindRDTexture  = link.KindRDTexture
)

var (
	ReadFrameHeader  = link.ReadFrameHeader
	DecodeCornerRGBA = link.DecodeCornerRGBA
)
