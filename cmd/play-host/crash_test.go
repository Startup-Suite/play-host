package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Stage 6 (task 01a0db5f), finding C: play-host.exe exited with code 2 and
// nothing in play-host.log, because the runtime's panic output went to a
// stderr nobody captured. These run the test binary as a child that sets up
// logging the way `serve` does and then panics, and read the log file.

const forced = "forced panic 01a0db5f"

func TestMain(m *testing.M) {
	switch os.Getenv("PLAY_HOST_TEST_PANIC") {
	case "goroutine":
		if _, err := setupLogging(os.Getenv("PLAY_HOST_TEST_LOGS")); err != nil {
			os.Exit(9)
		}
		go func() { panic(forced + " on a goroutine") }()
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "main":
		if _, err := setupLogging(os.Getenv("PLAY_HOST_TEST_LOGS")); err != nil {
			os.Exit(9)
		}
		func() {
			defer logPanic()
			panic(forced + " on main")
		}()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runPanicChild(t *testing.T, mode string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "PLAY_HOST_TEST_PANIC="+mode, "PLAY_HOST_TEST_LOGS="+dir)
	cmd.Stderr, cmd.Stdout = nil, nil // like the scheduled task: stderr goes nowhere
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "play-host.log"))
	return code, string(b)
}

func TestPanicOnAGoroutineIsWrittenToTheLog(t *testing.T) {
	code, logText := runPanicChild(t, "goroutine")
	if code != 2 {
		t.Errorf("exit code %d, want 2 (an unrecovered panic)", code)
	}
	for _, want := range []string{"panic: " + forced + " on a goroutine", "goroutine "} {
		if !strings.Contains(logText, want) {
			t.Errorf("play-host.log lacks %q:\n%s", want, logText)
		}
	}
}

func TestPanicOnMainIsLoggedWithItsStack(t *testing.T) {
	code, logText := runPanicChild(t, "main")
	if code != 2 {
		t.Errorf("exit code %d, want 2", code)
	}
	for _, want := range []string{"play-host: PANIC: " + forced + " on main", "logPanic"} {
		if !strings.Contains(logText, want) {
			t.Errorf("play-host.log lacks %q:\n%s", want, logText)
		}
	}
}
