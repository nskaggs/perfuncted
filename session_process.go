package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// managedSessionProcess owns waiting for a session infrastructure child. The
// wait goroutine is started immediately after the child starts, so a naturally
// exiting helper is reaped without waiting for session shutdown. Shutdown
// signals the process group and waits for this owner before tearing down the
// session runtime directory.
type managedSessionProcess struct {
	proc     *managedProc
	waitDone chan struct{}
	waitMu   sync.RWMutex
	waitErr  error
}

func newManagedSessionProcess(cmd *exec.Cmd) *managedSessionProcess {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	proc := &managedSessionProcess{
		proc: &managedProc{
			cmd: cmd,
			pid: cmd.Process.Pid,
		},
		waitDone: make(chan struct{}),
	}
	go proc.reap()
	return proc
}

func (p *managedSessionProcess) reap() {
	err := p.proc.cmd.Wait()
	p.waitMu.Lock()
	p.waitErr = err
	p.waitMu.Unlock()
	close(p.waitDone)
}

func (p *managedSessionProcess) wait(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: wait session process: %w: nil context", ErrInvalidArgument)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.waitDone:
		p.waitMu.RLock()
		defer p.waitMu.RUnlock()
		return p.waitErr
	}
}

func (p *managedSessionProcess) waitTimeout(timeout time.Duration) bool {
	if p == nil {
		return true
	}
	if timeout <= 0 {
		select {
		case <-p.waitDone:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.waitDone:
		return true
	case <-timer.C:
		return false
	}
}

func (p *managedSessionProcess) stop(waitTimeout time.Duration) {
	if p == nil || p.proc == nil {
		return
	}
	if err := p.proc.signal(syscall.SIGTERM); err != nil {
		slog.Debug("session: terminate process group", "pid", p.proc.pid, "error", err)
	}
	leaderExited := p.waitTimeout(waitTimeout)
	if leaderExited && p.proc.waitGroupTimeout(waitTimeout) {
		return
	}
	if err := p.proc.signal(syscall.SIGKILL); err != nil {
		slog.Debug("session: kill process group", "pid", p.proc.pid, "error", err)
	}
	p.waitTimeout(waitTimeout)
	_ = p.proc.waitGroupTimeout(waitTimeout)
}

type managedProc struct {
	cmd *exec.Cmd
	pid int

	mu        sync.Mutex
	groupGone bool
}

func (m *managedProc) stop(waitTimeout time.Duration) {
	if m == nil || m.pid <= 0 {
		return
	}
	if err := m.signal(syscall.SIGTERM); err != nil {
		slog.Debug("session: terminate process group", "pid", m.pid, "error", err)
	}
	leaderExited := false
	if m.cmd == nil {
		leaderExited = !pidAlive(m.pid)
	} else {
		leaderExited = waitForProc(m.pid, waitTimeout)
	}
	if leaderExited {
		if m.waitGroupTimeout(waitTimeout) {
			return
		}
	}
	if err := m.signal(syscall.SIGKILL); err != nil {
		slog.Debug("session: kill process group", "pid", m.pid, "error", err)
	}
	if m.cmd != nil {
		waitForProc(m.pid, waitTimeout)
	}
	_ = m.waitGroupTimeout(waitTimeout)
}

func processGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (m *managedProc) groupAlive() bool {
	if m == nil || m.pid <= 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.groupGone {
		return false
	}
	if !processGroupAlive(m.pid) {
		m.groupGone = true
		return false
	}
	return true
}

func (m *managedProc) signal(signal syscall.Signal) error {
	if m == nil || m.pid <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.groupGone {
		return nil
	}
	if !processGroupAlive(m.pid) {
		m.groupGone = true
		return nil
	}
	err := syscall.Kill(-m.pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		m.groupGone = true
		return nil
	}
	return err
}

func (m *managedProc) waitGroup(ctx context.Context) error {
	if m == nil || m.pid <= 0 {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: wait process group: %w: nil context", ErrInvalidArgument)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !m.groupAlive() {
			return nil
		}
		select {
		case <-ctx.Done():
			return contextErrorWithCause(ctx)
		case <-ticker.C:
		}
	}
}

// contextErrorWithCause keeps the standard cancellation/deadline category
// while retaining a caller-supplied cancellation cause for diagnostics.
func contextErrorWithCause(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	err := ctx.Err()
	if err == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, err) {
		return err
	}
	return errors.Join(err, cause)
}

func (m *managedProc) waitGroupTimeout(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !m.groupAlive() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForProc(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		var status syscall.WaitStatus
		waited, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		switch {
		case waited == pid:
			return true
		case errors.Is(err, syscall.ECHILD):
			return true
		case errors.Is(err, syscall.EINTR):
			continue
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
