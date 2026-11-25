package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/nskaggs/perfuncted/internal/contextutil"
)

// SessionProcess owns waiting for a session infrastructure child. The
// wait goroutine is started immediately after the child starts, so a naturally
// exiting helper is reaped without waiting for session shutdown. Shutdown
// signals the process group and waits for this owner before tearing down the
// session runtime directory.
type SessionProcess struct {
	proc     *Group
	waitDone chan struct{}
	waitMu   sync.RWMutex
	waitErr  error
}

// NewSessionProcess starts the sole asynchronous child reaper.
func NewSessionProcess(cmd *exec.Cmd) *SessionProcess {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	proc := &SessionProcess{
		proc: &Group{
			cmd: cmd,
			pid: cmd.Process.Pid,
		},
		waitDone: make(chan struct{}),
	}
	go proc.reap()
	return proc
}

// PID returns the owned infrastructure child leader.
func (p *SessionProcess) PID() int { return p.proc.PID() }

func (p *SessionProcess) reap() {
	err := p.proc.cmd.Wait()
	p.waitMu.Lock()
	p.waitErr = err
	p.waitMu.Unlock()
	close(p.waitDone)
}

// Wait observes the reaper result without transferring ownership.
func (p *SessionProcess) Wait(ctx context.Context, invalidArgument error) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: wait session process: %w: nil context", invalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return contextutil.ErrorWithCause(ctx)
	}
	select {
	case <-ctx.Done():
		return contextutil.ErrorWithCause(ctx)
	case <-p.waitDone:
		if err := ctx.Err(); err != nil {
			return contextutil.ErrorWithCause(ctx)
		}
		p.waitMu.RLock()
		defer p.waitMu.RUnlock()
		if err := ctx.Err(); err != nil {
			return contextutil.ErrorWithCause(ctx)
		}
		return p.waitErr
	}
}

// WaitTimeout observes whether the child has been reaped.
func (p *SessionProcess) WaitTimeout(timeout time.Duration) bool {
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

// Stop terminates the owned group with the existing escalation policy.
func (p *SessionProcess) Stop(waitTimeout time.Duration) {
	if p == nil || p.proc == nil {
		return
	}
	if err := p.proc.Signal(syscall.SIGTERM); err != nil {
		slog.Debug("session: terminate process group", "pid", p.proc.pid, "error", err)
	}
	leaderExited := p.WaitTimeout(waitTimeout)
	if leaderExited && p.proc.waitGroupTimeout(waitTimeout) {
		return
	}
	if err := p.proc.Signal(syscall.SIGKILL); err != nil {
		slog.Debug("session: kill process group", "pid", p.proc.pid, "error", err)
	}
	p.WaitTimeout(waitTimeout)
	_ = p.proc.waitGroupTimeout(waitTimeout)
}

// Group owns process-group signaling and retirement.
type Group struct {
	cmd *exec.Cmd
	pid int

	mu        sync.Mutex
	groupGone bool
}

// Stop terminates the owned group with the existing escalation policy.
func (m *Group) Stop(waitTimeout time.Duration) {
	if m == nil || m.pid <= 0 {
		return
	}
	if err := m.Signal(syscall.SIGTERM); err != nil {
		slog.Debug("session: terminate process group", "pid", m.pid, "error", err)
	}
	leaderExited := false
	if m.cmd == nil {
		leaderExited = !PIDAlive(m.pid)
	} else {
		leaderExited = WaitForProc(m.pid, waitTimeout)
	}
	if leaderExited {
		if m.waitGroupTimeout(waitTimeout) {
			return
		}
	}
	if err := m.Signal(syscall.SIGKILL); err != nil {
		slog.Debug("session: kill process group", "pid", m.pid, "error", err)
	}
	if m.cmd != nil {
		WaitForProc(m.pid, waitTimeout)
	}
	_ = m.waitGroupTimeout(waitTimeout)
}

// GroupAlive checks and permanently retires vanished ownership.
func (m *Group) GroupAlive() bool {
	if m == nil || m.pid <= 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.groupGone {
		return false
	}
	if !ProcessGroupAlive(m.pid) {
		m.groupGone = true
		return false
	}
	return true
}

// Signal signals only the still-owned process group.
func (m *Group) Signal(signal syscall.Signal) error {
	if m == nil || m.pid <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.groupGone {
		return nil
	}
	if !ProcessGroupAlive(m.pid) {
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

// WaitGroup waits for the owned group to disappear.
func (m *Group) WaitGroup(ctx context.Context, invalidArgument error) error {
	if m == nil || m.pid <= 0 {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: wait process group: %w: nil context", invalidArgument)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !m.GroupAlive() {
			return nil
		}
		select {
		case <-ctx.Done():
			return contextutil.ErrorWithCause(ctx)
		case <-ticker.C:
		}
	}
}

func (m *Group) waitGroupTimeout(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !m.GroupAlive() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// NewGroup records the already owned process-group leader.
func NewGroup(cmd *exec.Cmd, pid int) Group { return Group{cmd: cmd, pid: pid} }

// PID returns the owned leader PID.
func (m *Group) PID() int { return m.pid }

// WaitLeader reaps the leader through its exec.Cmd owner.
func (m *Group) WaitLeader() error { return m.cmd.Wait() }

// Retired reports whether group ownership has been permanently retired.
func (m *Group) Retired() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.groupGone }
