//go:build !windows

package launch

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type groupProc struct {
	cmd    *exec.Cmd
	mu     sync.Mutex
	exited bool // after Wait the pgid may be reused; Kill must not signal it
}

// Start runs the process in its own process group (development on linux).
func Start(s Spec) (Proc, error) {
	c := exec.Command(s.Path, s.Args...)
	c.Dir = s.Dir
	if s.LogPath != "" {
		f, err := os.Create(s.LogPath)
		if err != nil {
			return nil, err
		}
		c.Stdout, c.Stderr = f, f
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		return nil, err
	}
	return &groupProc{cmd: c}, nil
}

func (p *groupProc) Pid() int { return p.cmd.Process.Pid }

func (p *groupProc) Wait() (int, error) {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.exited = true
	p.mu.Unlock()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func (p *groupProc) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exited {
		return nil
	}
	return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
}

func (p *groupProc) Pids() ([]int, error) { return []int{p.cmd.Process.Pid}, nil }
