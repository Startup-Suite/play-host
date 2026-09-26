// Package launch starts Godot (and anything else the host owns) so that Stop
// kills exactly the tree the host started and nothing else.
//
// On Windows every process goes into its own Job Object, created SUSPENDED,
// assigned to the job, then resumed, so a child spawned in the first
// instruction (the Godot _console wrapper spawns the real exe immediately)
// is inside the job too. The job has KILL_ON_JOB_CLOSE, so a crashed host
// takes its Godot with it. Nothing is ever matched or killed by process name.
package launch

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
	// Kill terminates every process in the tree the host started.
	Kill() error
	// Pids lists the processes currently in the tree (Windows: the job).
	Pids() ([]int, error)
}
