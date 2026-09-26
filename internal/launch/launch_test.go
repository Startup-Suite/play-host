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
