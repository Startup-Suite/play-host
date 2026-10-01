package media

import (
	"encoding/binary"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPCMHasSignal(t *testing.T) {
	if PCMHasSignal(make([]byte, 32)) {
		t.Fatal("zero PCM reported as signal")
	}
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b[12:], 0x3f000000)
	if !PCMHasSignal(b) {
		t.Fatal("non-zero PCM reported as silence")
	}
}

func TestAudioCommandIsGamePCMOnly(t *testing.T) {
	s := strings.Join(AudioCommand(40331), " ")
	for _, want := range []string{"-f f32le", "-ar 48000", "-ac 2", "-i pipe:0", "-c:a libopus", "-frame_duration 20", "-payload_type 111", "rtp://127.0.0.1:40331?pkt_size=1200"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q: %s", want, s)
		}
	}
	for _, device := range []string{"dshow", "wasapi", "avfoundation", "pulse", "alsa", "desktop"} {
		if strings.Contains(strings.ToLower(s), device) {
			t.Errorf("audio command opens device %q: %s", device, s)
		}
	}
}

func TestAudioQueueIsBoundedAndDropsOldest(t *testing.T) {
	p := &AudioPipeline{cmd: &exec.Cmd{}, queue: make(chan []byte, AudioQueueChunks)}
	for i := 0; i < AudioQueueChunks+4; i++ {
		b := make([]byte, AudioChannels*4)
		b[0] = byte(i)
		if !p.Submit(b) {
			t.Fatalf("submit %d", i)
		}
	}
	if got := len(p.queue); got != AudioQueueChunks {
		t.Fatalf("queue %d", got)
	}
	if got := p.Dropped.Load(); got != 4 {
		t.Fatalf("dropped %d", got)
	}
	if oldest := (<-p.queue)[0]; oldest != 4 {
		t.Fatalf("oldest retained chunk %d", oldest)
	}
}

func TestAudioPipelineRestartKeepsRTPContinuousAndStops(t *testing.T) {
	exe, _ := os.Executable()
	t.Setenv("FAKE_FFMPEG", "1")
	s := &sink{}
	p := &AudioPipeline{FFmpeg: exe, RTPPort: 40392, Track: s}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		b := make([]byte, AudioChannels*4)
		b[0] = byte(i + 1)
		if !p.Submit(b) {
			t.Fatal("submit")
		}
	}
	waitN(t, s, 3)
	p.Stop()
	if p.Running() || p.Submit(make([]byte, AudioChannels*4)) {
		t.Fatal("audio pipeline active after Stop")
	}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if !p.Submit(make([]byte, AudioChannels*4)) {
			t.Fatal("restart submit")
		}
	}
	waitN(t, s, 5)
	p.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, pkt := range s.pkts {
		if pkt.PayloadType != 111 {
			t.Errorf("packet %d PT=%d", i, pkt.PayloadType)
		}
		if i > 0 && pkt.SequenceNumber != s.pkts[i-1].SequenceNumber+1 {
			t.Errorf("seq jump %d", i)
		}
		if i > 0 {
			d := pkt.Timestamp - s.pkts[i-1].Timestamp
			if d == 0 || d > AudioSampleRate*5 {
				t.Errorf("timestamp jump %d", d)
			}
		}
	}
}
