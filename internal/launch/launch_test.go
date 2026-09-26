//go:build !windows

package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKillStopsTreeAndLogsOutput(t *testing.T) {
	log := filepath.Join(t.TempDir(), "out.log")
	p, err := Start(Spec{Path: "/bin/sh", Args: []string{"-c", "echo started; sleep 30 & wait"}, LogPath: log})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()
	time.Sleep(200 * time.Millisecond)
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tree survived Kill")
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "started") {
		t.Fatalf("log %q", b)
	}
}

func TestGodotArgsCarryMarkerAfterDoubleDash(t *testing.T) {
	a := GodotArgs(`C:\co\01a0db5f-0123456789ab`, 1280, 720, "sess-1", 40320, 60)
	got := strings.Join(a, " ")
	want := `--path C:\co\01a0db5f-0123456789ab --rendering-driver vulkan --resolution 1280x720 --windowed -- --suite-play-session=sess-1 --suite-play-port=40320 --suite-play-fps=60`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	for _, bad := range []string{"--remote-debug", "--editor", "-e"} {
		for _, x := range a {
			if x == bad {
				t.Fatalf("%s in play args", bad)
			}
		}
	}
}
