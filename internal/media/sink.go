package media

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/h264reader"
)

// Counters are cheap stats for logs.
type Counters struct {
	Frames  atomic.Int64
	Packets atomic.Int64
	Bytes   atomic.Int64
}

// RTPWriter is the subset of TrackLocalStaticRTP the forwarder needs.
type RTPWriter interface {
	WriteRTP(*rtp.Packet) error
}

// ForwardRTP reads ffmpeg's RTP from conn and writes each packet to the
// track. pion rewrites SSRC and payload type per binding. A marker bit ends
// a frame. Returns when conn is closed.
func ForwardRTP(conn net.PacketConn, w RTPWriter, c *Counters) error {
	buf := make([]byte, 1600)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		var pkt rtp.Packet
		if err := pkt.Unmarshal(buf[:n]); err != nil {
			continue
		}
		c.Packets.Add(1)
		c.Bytes.Add(int64(n))
		if pkt.Marker {
			c.Frames.Add(1)
		}
		if err := w.WriteRTP(&pkt); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			return err
		}
	}
}

// SampleWriter is the subset of TrackLocalStaticSample the Annex-B path needs.
type SampleWriter interface {
	WriteSample(media.Sample) error
}

// AccessUnits groups Annex-B NALs from r into access units: parameter sets
// and SEI are held and flushed together with the next slice. One slice per
// frame (NVENC's default) means one call to emit per frame. Note that
// h264reader only returns a NAL once the NEXT start code has been read, so
// every frame waits for the first bytes of the following one.
func AccessUnits(r io.Reader, emit func(au []byte) error) error {
	hr, err := h264reader.NewReader(r)
	if err != nil {
		return err
	}
	var au []byte
	for {
		nal, err := hr.NextNAL()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		au = append(au, 0, 0, 0, 1)
		au = append(au, nal.Data...)
		switch nal.UnitType {
		case h264reader.NalUnitTypeCodedSliceNonIdr, h264reader.NalUnitTypeCodedSliceIdr:
			if err := emit(au); err != nil {
				return err
			}
			au = nil
		}
	}
}

// ForwardAnnexB feeds access units from r into the sample track at fps.
func ForwardAnnexB(r io.Reader, w SampleWriter, fps int, c *Counters) error {
	d := time.Second / time.Duration(fps)
	return AccessUnits(r, func(au []byte) error {
		c.Frames.Add(1)
		c.Bytes.Add(int64(len(au)))
		if err := w.WriteSample(media.Sample{Data: au, Duration: d}); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			return err
		}
		return nil
	})
}

var _ RTPWriter = (*webrtc.TrackLocalStaticRTP)(nil)
var _ SampleWriter = (*webrtc.TrackLocalStaticSample)(nil)
