package link

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestPCMHeaderRoundTripAndTruncation(t *testing.T) {
	in := PCMHeader{SampleRate: PCMSampleRate, Channels: PCMChannels, Format: PCMFormatF32LE, Frames: 960, Length: 960 * 2 * 4}
	var b bytes.Buffer
	if err := WritePCMHeader(&b, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadPCMHeader(&b)
	if err != nil || out != in {
		t.Fatalf("%+v %v", out, err)
	}

	b.Reset()
	_ = WritePCMHeader(&b, in)
	payload := make([]byte, in.Length)
	binary.LittleEndian.PutUint32(payload[4:], 0x3f000000)
	b.Write(payload[:len(payload)-1])
	if _, err := ReadPCMHeader(&b); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(&b, payload); err != io.ErrUnexpectedEOF {
		t.Fatalf("payload truncation: %v", err)
	}

	for _, bad := range []PCMHeader{
		{SampleRate: 44100, Channels: 2, Format: 1, Frames: 960, Length: 7680},
		{SampleRate: 48000, Channels: 1, Format: 1, Frames: 960, Length: 3840},
		{SampleRate: 48000, Channels: 2, Format: 1, Frames: 960, Length: 1},
		{SampleRate: 48000, Channels: 2, Format: 1, Frames: MaxPCMFrames + 1, Length: (MaxPCMFrames + 1) * 8},
	} {
		b.Reset()
		_ = WritePCMHeader(&b, bad)
		if _, err := ReadPCMHeader(&b); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
