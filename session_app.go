package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/nskaggs/perfuncted/v2/internal/env"
	sessionruntime "github.com/nskaggs/perfuncted/v2/internal/runtime"

	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
)

var sessionRoutingKeys = []string{
	"XDG_RUNTIME_DIR",
	"WAYLAND_DISPLAY",
	"DBUS_SESSION_BUS_ADDRESS",
	"XDG_SESSION_TYPE",
	"DISPLAY",
	"SWAYSOCK",
	"HYPRLAND_INSTANCE_SIGNATURE",
	"AT_SPI_BUS",
	"ATSPI_BUS_ADDRESS",
	"AT_SPI_BUS_ADDRESS",
}

// Command describes an external program to launch as an Application.
type Command struct {
	// Name is the executable to start.
	Name string
	// Args contains arguments passed to Name.
	Args []string
	// Dir is the child working directory.
	Dir string
	// Env supplies the complete child environment base when non-nil. The
	// session environment is used when Env is nil, and authoritative session
	// routing keys are overlaid in both cases. An empty, non-nil slice therefore
	// intentionally launches with only the routing keys present in the session.
	Env []string
	// Stdin supplies standard input to the child.
	Stdin io.Reader
	// Stdout receives child standard output.
	Stdout io.Writer
	// Stderr receives child standard error.
	Stderr io.Writer
}

// Application is a process group launched and owned by a Session.
type applicationState struct {
	session *sessionCore
	proc    sessionruntime.Group
	path    string

	done chan struct{}
	mu   sync.RWMutex
	err  error
}

// Launch starts cmd as a managed process group. ctx governs startup only:
// cancelling it after Launch returns does not terminate the application.
func (s *sessionCore) Launch(
	ctx context.Context,
	command Command,
) (*applicationState, error) {
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
	if command.Env == nil {
		// The session environment is already a host snapshot with managed
		// routing applied. Use it directly so deleted routing keys cannot be
		// reintroduced from the live parent process.
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
	if s.closed || s.ctx == nil {
		return nil, ErrSessionClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := execCommand.Start(); err != nil {
		return nil, fmt.Errorf("perfuncted: launch %q: %w", command.Name, err)
	}

	app := &applicationState{
		session: s,
		proc:    sessionruntime.NewGroup(execCommand, execCommand.Process.Pid),
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

func (s *sessionCore) routingEnvironment() []string {
	overlays := make([]string, 0, len(sessionRoutingKeys))
	for _, key := range sessionRoutingKeys {
		if val, ok := s.env.Lookup(key); ok {
			overlays = append(overlays, key+"="+val)
		}
	}
	return overlays
}

func (a *applicationState) reap() {
	err := a.proc.WaitLeader()
	a.mu.Lock()
	a.err = err
	a.mu.Unlock()
	close(a.done)
	a.session.notifyWaiters()
}

// Wait waits for the process-group leader to exit. It is safe to call
// repeatedly or concurrently; Stop and Session.Close additionally wait for
// the entire owned process group.
func (a *applicationState) Wait(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: application wait: %w: nil context", ErrInvalidArgument)
	}
	// When both the process exit and cancellation are ready, select alone
	// chooses nondeterministically. A canceled wait must not report success.
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		a.mu.RLock()
		defer a.mu.RUnlock()
		return a.err
	}
}

// PID returns the process-group leader's PID.
func (a *applicationState) PID() int {
	if a == nil {
		return 0
	}
	return a.proc.PID()
}

// Path returns the resolved executable path.
func (a *applicationState) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// Exited reports whether the process-group leader has been reaped. Descendants
// may still be alive until Stop or Session.Close completes.
func (a *applicationState) Exited() bool {
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
func (a *applicationState) Stop(ctx context.Context) error {
	if a == nil || a.proc.PID() <= 0 {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: stop application: %w: nil context", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return contextutil.ErrorWithCause(ctx)
	}
	if err := a.proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("perfuncted: stop application %d: %w", a.proc.PID(), err)
	}
	if err := a.proc.WaitGroup(ctx, ErrInvalidArgument); err != nil {
		return err
	}
	return nil
}

// Kill sends SIGKILL to the application process group.
func (a *applicationState) Kill() error {
	if a == nil || a.proc.PID() <= 0 {
		return nil
	}
	if err := a.proc.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("perfuncted: kill application %d: %w", a.proc.PID(), err)
	}
	return nil
}

func (s *sessionCore) stopApplication(app *applicationState) error {
	if app == nil {
		return nil
	}
	grace := s.config.ApplicationGracePeriod
	if grace <= 0 {
		grace = s.config.Timeouts.WithDefaults().Short
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
	if err := app.proc.WaitGroup(killCtx, ErrInvalidArgument); err != nil {
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

func (a *applicationState) ownsPID(pid int32) bool {
	if a == nil || pid <= 0 || !a.proc.GroupAlive() {
		return false
	}
	pgid, err := syscall.Getpgid(int(pid))
	return err == nil && pgid == a.proc.PID()
}

// LogPath returns the unique private log directory used by owned session
// infrastructure. The directory is retained for bounded post-failure
// inspection and is cleaned up by CleanupSessionLogs when it expires.
func (s *sessionCore) LogPath() string {
	if s == nil || s.infra == nil {
		return ""
	}
	return s.infra.logDir
}

// SwaySocket returns the owned Sway IPC socket, when present.
func (s *sessionCore) SwaySocket() string {
	if s == nil || s.infra == nil {
		return ""
	}
	return sessionruntime.SwaySocketPath(s.infra.xdgDir)
}

// Application is a detached, non-owning handle to a session-owned process group.
type Application struct{ state *applicationState }

func (a *Application) privateState() *applicationState {
	if a == nil {
		return nil
	}
	return a.state
}

func wrapApplication(a *applicationState) *Application {
	if a == nil {
		return nil
	}
	return &Application{state: a}
}

// Launch starts cmd as a managed process group. ctx governs startup only:
// cancelling it after Launch returns does not terminate the application.
func (s *Session) Launch(
	ctx context.Context,
	command Command,
) (*Application, error) {
	v, err := s.coreState().Launch(ctx, command)
	return wrapApplication(v), err
}

// Wait waits for the process-group leader to exit. It is safe to call
// repeatedly or concurrently; Stop and Session.Close additionally wait for
// the entire owned process group.
func (a *Application) Wait(ctx context.Context) error {
	return a.privateState().Wait(ctx)
}

// PID returns the process-group leader's PID.
func (a *Application) PID() int {
	return a.privateState().PID()
}

// Path returns the resolved executable path.
func (a *Application) Path() string {
	return a.privateState().Path()
}

// Exited reports whether the process-group leader has been reaped. Descendants
// may still be alive until Stop or Session.Close completes.
func (a *Application) Exited() bool {
	return a.privateState().Exited()
}

// Stop sends SIGTERM to the application process group and waits for ctx.
func (a *Application) Stop(ctx context.Context) error {
	return a.privateState().Stop(ctx)
}

// Kill sends SIGKILL to the application process group.
func (a *Application) Kill() error {
	return a.privateState().Kill()
}

// LogPath returns the unique private log directory used by owned session
// infrastructure. The directory is retained for bounded post-failure
// inspection and is cleaned up by CleanupSessionLogs when it expires.
func (s *Session) LogPath() string {
	return s.coreState().LogPath()
}

// SwaySocket returns the owned Sway IPC socket, when present.
func (s *Session) SwaySocket() string {
	return s.coreState().SwaySocket()
}

// WaitForWindow waits for a window belonging to the application's process
// group. When the backend has no PID data, match must identify one window.
func (a *Application) WaitForWindow(
	ctx context.Context,
	match WindowMatch,
) (*Window, error) {
	v, err := a.privateState().WaitForWindow(ctx, match)
	return wrapWindow(v), err
}
