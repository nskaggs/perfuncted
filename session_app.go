package perfuncted

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nskaggs/perfuncted/internal/executil"
)

// Command describes an external program to launch as an Application.
type Command struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

// Application tracks a launched external process.
type Application struct {
	session *Session
	cmd     *exec.Cmd
	pid     int
	path    string
}

// Launch starts the given command as a managed application within the session.
// The returned Application can be used to wait for exit, inspect the PID, or
// retrieve the log path.
func (s *Session) Launch(ctx context.Context, cmd Command) (*Application, error) {
	if s == nil {
		return nil, ErrNilSession
	}

	resolved, err := exec.LookPath(cmd.Name)
	if err != nil {
		return nil, fmt.Errorf("launch: look path %s: %w", cmd.Name, err)
	}

	args := append([]string{cmd.Name}, cmd.Args...)
	execCmd := executil.CommandContext(ctx, resolved, args...)
	if cmd.Dir != "" {
		execCmd.Dir = cmd.Dir
	}
	execCmd.Env = cmd.Env
	execCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := execCmd.Start(); err != nil {
		return nil, fmt.Errorf("launch: start: %w", err)
	}

	app := &Application{
		session: s,
		cmd:     execCmd,
		pid:     execCmd.Process.Pid,
		path:    resolved,
	}

	s.appMu.Lock()
	s.apps[app] = struct{}{}
	s.appMu.Unlock()

	return app, nil
}

// Wait blocks until the application exits and returns its exit status.
func (a *Application) Wait(ctx context.Context) error {
	if a == nil || a.cmd == nil {
		return nil
	}
	err := a.cmd.Wait()

	a.session.appMu.Lock()
	delete(a.session.apps, a)
	a.session.appMu.Unlock()

	return err
}

// PID returns the process ID of the running application.
func (a *Application) PID() int {
	if a == nil {
		return 0
	}
	return a.pid
}

// Path returns the resolved executable path.
func (a *Application) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// LogPath returns the log directory used by the session infrastructure,
// or an empty string if not available.
func (s *Session) LogPath() string {
	if s == nil || s.infra == nil {
		return ""
	}
	return s.infra.logDir
}

// Stop kills all running applications and tears down the session.
func (s *Session) Stop() {
	if s == nil {
		return
	}
	s.appMu.Lock()
	apps := make([]*Application, 0, len(s.apps))
	for app := range s.apps {
		apps = append(apps, app)
	}
	s.appMu.Unlock()

	for _, app := range apps {
		if app.cmd != nil && app.cmd.Process != nil {
			syscall.Kill(-app.pid, syscall.SIGTERM) //nolint:errcheck
		}
	}

	deadline := time.After(2 * time.Second)
	for {
		s.appMu.Lock()
		remaining := len(s.apps)
		s.appMu.Unlock()
		if remaining == 0 {
			break
		}
		select {
		case <-deadline:
			s.appMu.Lock()
			for app := range s.apps {
				if app.cmd != nil && app.cmd.Process != nil {
					syscall.Kill(-app.pid, syscall.SIGKILL) //nolint:errcheck
				}
			}
			s.appMu.Unlock()
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// SwaySocket returns the sway IPC socket path for the session, or empty
// string if not a sway-based session.
func (s *Session) SwaySocket() string {
	if s == nil || s.infra == nil {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(s.infra.xdgDir, "sway-ipc.*.sock"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}
