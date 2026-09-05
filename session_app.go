package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

const defaultApplicationGracePeriod = 2 * time.Second

var sessionRoutingKeys = []string{
	"XDG_RUNTIME_DIR",
	"WAYLAND_DISPLAY",
	"DBUS_SESSION_BUS_ADDRESS",
	"XDG_SESSION_TYPE",
	"DISPLAY",
	"SWAYSOCK",
	"HYPRLAND_INSTANCE_SIGNATURE",
	"GDK_BACKEND",
	"QT_QPA_PLATFORM",
}

// Command describes an external program to launch as an Application.
type Command struct {
	// Name is the executable to start.
	Name string
	// Args contains arguments passed to Name.
	Args []string
	// Dir is the child working directory.
	Dir string
	// Env supplies child environment entries in addition to session routing values.
	Env []string
	// Stdin supplies standard input to the child.
	Stdin io.Reader
	// Stdout receives child standard output.
	Stdout io.Writer
	// Stderr receives child standard error.
	Stderr io.Writer
}

// Application is a process group launched and owned by a Session.
type Application struct {
	session *Session
	proc    managedProc
	path    string

	done chan struct{}
	mu   sync.RWMutex
	err  error
}

// Launch starts cmd as a managed process group. ctx governs startup only:
// cancelling it after Launch returns does not terminate the application.
func (s *Session) Launch(
	ctx context.Context,
	command Command,
) (*Application, error) {
	if s == nil {
		return nil, ErrNilSession
	}
	if ctx == nil {
		return nil, fmt.Errorf("perfuncted: launch: %w: nil context", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if command.Name == "" {
		return nil, fmt.Errorf("perfuncted: launch: %w: command name is empty", ErrInvalidArgument)
	}

	resolved, err := exec.LookPath(command.Name)
	if err != nil {
		return nil, fmt.Errorf("perfuncted: launch %q: %w", command.Name, err)
	}

	execCommand := exec.Command(resolved, command.Args...)
	execCommand.Dir = command.Dir
	baseEnvironment := commandEnvironment(command.Env)
	if command.Env == nil && s.target.Kind() == TargetExplicit {
		// An explicit target is an immutable environment snapshot. Do not
		// silently reintroduce host variables when the caller leaves Env nil.
		baseEnvironment = s.env.EnvList()
	}
	execCommand.Env = env.Merge(
		baseEnvironment,
		s.routingEnvironment()...,
	)
	execCommand.Stdin = command.Stdin
	execCommand.Stdout = command.Stdout
	execCommand.Stderr = command.Stderr
	execCommand.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed {
		return nil, ErrSessionClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := execCommand.Start(); err != nil {
		return nil, fmt.Errorf("perfuncted: launch %q: %w", command.Name, err)
	}

	app := &Application{
		session: s,
		proc:    managedProc{cmd: execCommand, pid: execCommand.Process.Pid},
		path:    resolved,
		done:    make(chan struct{}),
	}
	s.apps = append(s.apps, app)
	go app.reap()
	return app, nil
}

func commandEnvironment(values []string) []string {
	if values == nil {
		return os.Environ()
	}
	return values
}

func (s *Session) routingEnvironment() []string {
	overlays := make([]string, 0, len(sessionRoutingKeys))
	for _, key := range sessionRoutingKeys {
		if val, ok := s.env.Lookup(key); ok {
			overlays = append(overlays, key+"="+val)
		}
	}
	return overlays
}

func (a *Application) reap() {
	err := a.proc.cmd.Wait()
	a.mu.Lock()
	a.err = err
	a.mu.Unlock()
	close(a.done)
	a.session.notifyWaiters()
}

// Wait waits for the process-group leader to exit. It is safe to call
// repeatedly or concurrently; Stop and Session.Close additionally wait for
// the entire owned process group.
func (a *Application) Wait(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: application wait: %w: nil context", ErrInvalidArgument)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		a.mu.RLock()
		defer a.mu.RUnlock()
		return a.err
	}
}

// PID returns the process-group leader's PID.
func (a *Application) PID() int {
	if a == nil {
		return 0
	}
	return a.proc.pid
}

// Path returns the resolved executable path.
func (a *Application) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// Exited reports whether the process-group leader has been reaped. Descendants
// may still be alive until Stop or Session.Close completes.
func (a *Application) Exited() bool {
	if a == nil {
		return true
	}
	select {
	case <-a.done:
		return true
	default:
		return false
	}
}

// Stop sends SIGTERM to the application process group and waits for ctx.
func (a *Application) Stop(ctx context.Context) error {
	if a == nil || a.proc.pid <= 0 {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: stop application: %w: nil context", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.proc.signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("perfuncted: stop application %d: %w", a.proc.pid, err)
	}
	if err := a.proc.waitGroup(ctx); err != nil {
		return err
	}
	return nil
}

// Kill sends SIGKILL to the application process group.
func (a *Application) Kill() error {
	if a == nil || a.proc.pid <= 0 {
		return nil
	}
	if err := a.proc.signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("perfuncted: kill application %d: %w", a.proc.pid, err)
	}
	return nil
}

func (s *Session) stopApplication(app *Application) error {
	if app == nil {
		return nil
	}
	grace := s.config.ApplicationGracePeriod
	if grace <= 0 {
		grace = defaultApplicationGracePeriod
	}
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := app.Stop(ctx); err == nil {
		return nil
	} else if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if err := app.Kill(); err != nil {
		return err
	}
	killCtx, killCancel := context.WithTimeout(
		context.Background(),
		grace,
	)
	defer killCancel()
	if err := app.proc.waitGroup(killCtx); err != nil {
		return err
	}
	if err := app.Wait(killCtx); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	}
	return nil
}

func (a *Application) ownsPID(pid int32) bool {
	if a == nil || pid <= 0 || !a.proc.groupAlive() {
		return false
	}
	pgid, err := syscall.Getpgid(int(pid))
	return err == nil && pgid == a.proc.pid
}

// LogPath returns the unique private log directory used by owned session
// infrastructure. The directory is retained for bounded post-failure
// inspection and is cleaned up by CleanupSessionLogs when it expires.
func (s *Session) LogPath() string {
	if s == nil || s.infra == nil {
		return ""
	}
	return s.infra.logDir
}

// SwaySocket returns the owned Sway IPC socket, when present.
func (s *Session) SwaySocket() string {
	if s == nil || s.infra == nil {
		return ""
	}
	matches, err := filepath.Glob(
		filepath.Join(s.infra.xdgDir, "sway-ipc.*.sock"),
	)
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}
