// Package launch starts Godot (and anything else the host owns) so that Stop
// kills exactly the tree the host started and nothing else. Windows retains
// its Job Object path; Unix platforms use an owned process group.
package launch

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// Spec is one process to start.
type Spec struct {
	Path    string
	Args    []string // not including argv[0]
	Dir     string
	LogPath string // stdout+stderr file; empty = discard
}

// Proc is a started process tree.
type Proc interface {
	Pid() int
	Wait() (exitCode int, err error)
	Kill() error
	Pids() ([]int, error)
}

// DefaultRenderingDriver preserves the proven explicit Vulkan selection on
// Windows (and other non-Darwin hosts). On Darwin it is omitted so Godot picks
// its platform default; config may still request an explicit driver.
func DefaultRenderingDriver(goos string) string {
	if goos == "darwin" {
		return ""
	}
	return "vulkan"
}

// GodotExecutable expands a macOS .app bundle to its conventional executable.
// Already-expanded paths, PATH lookups ("godot"), Windows executable paths,
// and paths containing spaces are returned unchanged.
func GodotExecutable(path, goos string) string {
	if goos != "darwin" || !strings.EqualFold(filepath.Ext(path), ".app") {
		return path
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return filepath.Join(path, "Contents", "MacOS", base)
}

// HostGodotExecutable resolves an executable for the running platform.
func HostGodotExecutable(path string) string { return GodotExecutable(path, runtime.GOOS) }

// GodotArgs is the play command line using the host's native driver.
func GodotArgs(dir string, w, h int, session string, port, fps int) []string {
	return GodotArgsForDriver(dir, DefaultRenderingDriver(runtime.GOOS), w, h, session, port, fps)
}

// GodotArgsForDriver builds the portable play command line. The project path
// is kept as one argv element; no shell quoting or platform-specific separator
// rewriting is involved.
func GodotArgsForDriver(dir, driver string, w, h int, session string, port, fps int) []string {
	a := []string{"--path", dir}
	if driver != "" {
		a = append(a, "--rendering-driver", driver)
	}
	a = append(a, "--resolution", fmt.Sprintf("%dx%d", w, h), "--windowed")
	return append(a, "--", "--suite-play-session="+session, fmt.Sprintf("--suite-play-port=%d", port), fmt.Sprintf("--suite-play-fps=%d", fps))
}
