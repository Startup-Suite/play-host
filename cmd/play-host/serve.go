package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/Startup-Suite/play-host/internal/build"
	"github.com/Startup-Suite/play-host/internal/host"
	"github.com/Startup-Suite/play-host/internal/input"
	"github.com/Startup-Suite/play-host/internal/suite"
)

// Version is stamped at build time with -ldflags "-X main.Version=<sha>".
var Version = "dev"

// FileConfig is C:\Users\slaps\play-host\config.json on wave.
type FileConfig struct {
	SuiteURL        string       `json:"suite_url"`  // ws(s)://host[:port]/runtime/ws
	RuntimeID       string       `json:"runtime_id"` // play-host-wave
	TokenFile       string       `json:"token_file"` // secrets\runtime-token (file only)
	Godot           string       `json:"godot"`      // Godot *_console.exe
	FFmpeg          string       `json:"ffmpeg"`
	Git             string       `json:"git"`
	SSH             string       `json:"ssh"` // Git for Windows usr\bin\ssh.exe
	KnownHosts      string       `json:"known_hosts"`
	MirrorsDir      string       `json:"mirrors_dir"`
	CheckoutsDir    string       `json:"checkouts_dir"`
	AddonDir        string       `json:"addon_dir"`
	LogsDir         string       `json:"logs_dir"`
	HostIP          string       `json:"host_ip"`
	UDPMin          uint16       `json:"udp_min"`
	UDPMax          uint16       `json:"udp_max"`
	GodotPort       int          `json:"godot_port"`
	RTPPort         int          `json:"rtp_port"`
	ImportTimeoutS  int          `json:"import_timeout_s"`
	KeepCheckouts   int          `json:"keep_checkouts"`
	Repos           []build.Repo `json:"repos"`
	HeartbeatS      int          `json:"heartbeat_s"`
	LaunchTimeoutS  int          `json:"launch_timeout_s"`
	ProgressEveryMs int          `json:"progress_every_ms"`
}

// LoadConfig reads and checks the config file.
func LoadConfig(path string) (FileConfig, error) {
	var c FileConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if c.UDPMin == 0 {
		c.UDPMin, c.UDPMax = 40300, 40309
	}
	if c.GodotPort == 0 {
		c.GodotPort = 40320
	}
	if c.RTPPort == 0 {
		c.RTPPort = 40330
	}
	for name, v := range map[string]string{"suite_url": c.SuiteURL, "runtime_id": c.RuntimeID, "token_file": c.TokenFile,
		"godot": c.Godot, "ffmpeg": c.FFmpeg, "git": c.Git, "mirrors_dir": c.MirrorsDir, "checkouts_dir": c.CheckoutsDir,
		"addon_dir": c.AddonDir, "logs_dir": c.LogsDir} {
		if v == "" {
			return c, fmt.Errorf("%s: %s is required", path, name)
		}
	}
	if len(c.Repos) == 0 {
		return c, fmt.Errorf("%s: repos is empty; the host would refuse every session", path)
	}
	return c, nil
}

func (c FileConfig) hostConfig() host.Config {
	return host.Config{
		Godot: c.Godot, FFmpeg: c.FFmpeg, LogsDir: c.LogsDir, HostIP: c.HostIP, UDPMin: c.UDPMin, UDPMax: c.UDPMax,
		GodotPort: c.GodotPort, RTPPort: c.RTPPort,
		ImportTimeout: time.Duration(c.ImportTimeoutS) * time.Second,
		LinkTimeout:   time.Duration(c.LaunchTimeoutS) * time.Second,
		ProgressEvery: time.Duration(c.ProgressEveryMs) * time.Millisecond,
		Build: build.Config{Git: c.Git, SSH: c.SSH, KnownHosts: c.KnownHosts, MirrorsDir: c.MirrorsDir,
			CheckoutsDir: c.CheckoutsDir, AddonDir: c.AddonDir, Repos: c.Repos, KeepCheckouts: c.KeepCheckouts},
	}
}

func serve(args []string) error {
	if len(args) != 2 || args[0] != "-config" {
		return fmt.Errorf("usage: play-host serve -config <config.json>")
	}
	c, err := LoadConfig(args[1])
	if err != nil {
		return err
	}
	lf, err := setupLogging(c.LogsDir)
	if err != nil {
		return err
	}
	defer lf.Close()
	defer logPanic()
	log.Printf("play-host %s serve: runtime %s via %s", Version, c.RuntimeID, c.SuiteURL)

	hc := c.hostConfig()
	hc.PionLog = log.Writer()
	h := host.New(hc, nil, nil, log.Printf)
	cl := suite.New(suite.Config{URL: c.SuiteURL, RuntimeID: c.RuntimeID, TokenFile: c.TokenFile, Product: "play-host",
		Version: Version, Heartbeat: time.Duration(c.HeartbeatS) * time.Second}, h)
	h.SetSender(cl)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	cl.Run(ctx)
	if s := h.Current(); s != nil {
		h.OnDisconnected(fmt.Errorf("play-host stopping"))
		time.Sleep(2 * time.Second)
	}
	return nil
}

// setupLogging opens <logs>/play-host.log for the standard logger AND for
// the Go runtime's crash output (stage 6). The scheduled task that runs
// play-host does not redirect stderr, so before this an unrecovered panic
// on any goroutine killed the process (exit code 2) with its message and
// stack written nowhere. debug.SetCrashOutput makes the runtime copy every
// fatal panic, with all goroutine stacks, into the log file as well.
func setupLogging(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(filepath.Join(dir, "play-host.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	log.SetOutput(io.MultiWriter(os.Stderr, lf))
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.LUTC)
	if err := debug.SetCrashOutput(lf, debug.CrashOptions{}); err != nil {
		log.Printf("play-host: crash output not redirected: %v", err)
	}
	return lf, nil
}

// logPanic is the main goroutine's deferred recover: it logs the panic and
// its stack, then exits 2 as an unrecovered panic would, so the scheduled
// task still sees a failure. Panics on other goroutines are covered by
// SetCrashOutput (and the session's own guards turn most of them into a
// failed session instead).
func logPanic() {
	if r := recover(); r != nil {
		log.Printf("play-host: PANIC: %v\n%s", r, debug.Stack())
		os.Exit(2)
	}
}

func keynames() error {
	return json.NewEncoder(os.Stdout).Encode(input.KeyNames())
}
