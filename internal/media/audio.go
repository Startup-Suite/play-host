package media

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	AudioSampleRate  = 48000
	AudioChannels    = 2
	AudioFrameMillis = 20
	AudioQueueChunks = 8 // at most 160 ms queued; oldest audio is dropped first
)

// AudioCommand builds the deterministic game-PCM -> Opus/RTP ffmpeg command.
// Its only input is stdin; it never opens a microphone or desktop device.
func AudioCommand(rtpPort int) []string {
	return []string{
		"-hide_banner", "-loglevel", "warning", "-nostats", "-y",
		"-f", "f32le", "-ar", strconv.Itoa(AudioSampleRate), "-ac", strconv.Itoa(AudioChannels),
		"-use_wallclock_as_timestamps", "1", "-fflags", "nobuffer", "-i", "pipe:0",
		"-c:a", "libopus", "-application", "audio", "-frame_duration", strconv.Itoa(AudioFrameMillis),
		"-b:a", "128k", "-vbr", "off", "-compression_level", "5",
		"-f", "rtp", "-payload_type", "111",
		fmt.Sprintf("rtp://127.0.0.1:%d?pkt_size=1200", rtpPort),
	}
}

// PCMHasSignal distinguishes exact/near silence from audible PCM without
// changing it. It is also useful in transport tests and diagnostics.
func PCMHasSignal(b []byte) bool {
	for len(b) >= 4 {
		v := math.Float32frombits(binary.LittleEndian.Uint32(b))
		if !math.IsNaN(float64(v)) && math.Abs(float64(v)) > 1e-6 {
			return true
		}
		b = b[4:]
	}
	return false
}

// AudioPipeline owns one libopus ffmpeg process and its loopback RTP socket.
// Submit is non-blocking: the queue is bounded and drops the oldest chunk so
// stale audio never accumulates latency behind video or a slow encoder.
type AudioPipeline struct {
	FFmpeg  string
	RTPPort int
	Track   RTPWriter
	LogPath string
	Rewrite *Rewriter
	Counts  *Counters

	Dropped atomic.Int64
	Silent  atomic.Int64
	Signal  atomic.Int64

	mu    sync.Mutex
	cmd   *exec.Cmd
	stdin io.WriteCloser
	conn  net.PacketConn
	queue chan []byte
	stop  chan struct{}
	done  chan struct{}
	wg    sync.WaitGroup
	Args  []string
}

// Start allocates the UDP socket and starts ffmpeg and its single PCM writer.
func (p *AudioPipeline) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		return errors.New("audio pipeline already running")
	}
	if p.Rewrite == nil {
		p.Rewrite = NewRewriterClock(AudioSampleRate)
	}
	if p.Counts == nil {
		p.Counts = &Counters{}
	}
	conn, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", p.RTPPort))
	if err != nil {
		return fmt.Errorf("audio rtp listen: %w", err)
	}
	p.Args = AudioCommand(p.RTPPort)
	cmd := exec.Command(p.FFmpeg, p.Args...)
	var logf *os.File
	if p.LogPath != "" {
		if logf, err = os.OpenFile(p.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			fmt.Fprintf(logf, "%s ffmpeg %q\n", time.Now().UTC().Format(time.RFC3339), p.Args)
			cmd.Stderr = logf
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		conn.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		conn.Close()
		if logf != nil {
			logf.Close()
		}
		return fmt.Errorf("audio ffmpeg: %w", err)
	}
	q := make(chan []byte, AudioQueueChunks)
	stop := make(chan struct{})
	done := make(chan struct{})
	p.Rewrite.NewEpoch()
	p.cmd, p.stdin, p.conn, p.queue, p.stop, p.done = cmd, stdin, conn, q, stop, done
	p.wg.Add(2)
	go func() {
		defer p.wg.Done()
		_ = ForwardRTP(conn, rewriting{p.Track, p.Rewrite}, p.Counts)
	}()
	go func() {
		defer p.wg.Done()
		defer stdin.Close()
		for {
			select {
			case b := <-q:
				if _, err := stdin.Write(b); err != nil {
					return
				}
			case <-stop:
				return
			}
		}
	}()
	go func() {
		_ = cmd.Wait()
		conn.Close()
		if logf != nil {
			logf.Close()
		}
		close(done)
	}()
	return nil
}

// Submit queues interleaved stereo f32le PCM without blocking. It returns
// false if the pipeline is stopped or the payload cannot contain whole frames.
func (p *AudioPipeline) Submit(b []byte) bool {
	if len(b) == 0 || len(b)%(AudioChannels*4) != 0 {
		return false
	}
	c := append([]byte(nil), b...)
	p.mu.Lock()
	q, running := p.queue, p.cmd != nil
	p.mu.Unlock()
	if !running {
		return false
	}
	if PCMHasSignal(c) {
		p.Signal.Add(1)
	} else {
		p.Silent.Add(1)
	}
	select {
	case q <- c:
		return true
	default:
	}
	select {
	case <-q:
		p.Dropped.Add(1)
	default:
	}
	select {
	case q <- c:
		return true
	default:
		p.Dropped.Add(1)
		return false
	}
}

// Running reports whether the encoder process is alive.
func (p *AudioPipeline) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// Stop tears down the queue writer, ffmpeg process and RTP socket. It is safe
// to call repeatedly and a subsequent Start creates an independent epoch.
func (p *AudioPipeline) Stop() {
	p.mu.Lock()
	cmd, in, stop, done := p.cmd, p.stdin, p.stop, p.done
	p.cmd, p.stdin, p.conn, p.queue, p.stop, p.done = nil, nil, nil, nil, nil, nil
	p.mu.Unlock()
	if cmd == nil {
		return
	}
	close(stop)
	_ = in.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	p.wg.Wait()
}
