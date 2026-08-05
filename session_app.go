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
	Name   string
	Args   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Application is a process group launched and owned by a Session.
type Application struct {
	session *Session
	cmd     *exec.Cmd
	pid     int
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
		return nil, errors.New("perfuncted: launch: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if command.Name == "" {
		return nil, errors.New("perfuncted: launch: command name is empty")
	}

	resolved, err := exec.LookPath(command.Name)
	if err != nil {
		return nil, fmt.Errorf("perfuncted: launch %q: %w", command.Name, err)
	}

	execCommand := exec.Command(resolved, command.Args...)
	execCommand.Dir = command.Dir
	execCommand.Env = env.Merge(
		commandEnvironment(command.Env),
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
		cmd:     execCommand,
		pid:     execCommand.Process.Pid,
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
		overlays = append(overlays, key+"="+s.env.Get(key))
	}
	return overlays
}

func (a *Application) reap() {
	err := a.cmd.Wait()
	a.mu.Lock()
	a.err = err
	a.mu.Unlock()
	close(a.done)
	a.session.notifyWaiters()
}

// Wait waits for the application to exit. It is safe to call repeatedly or
// concurrently.
func (a *Application) Wait(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("perfuncted: application wait: nil context")
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
	return a.pid
}

// Path returns the resolved executable path.
func (a *Application) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// Exited reports whether the application has been reaped.
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
	if a == nil || a.pid <= 0 {
		return nil
	}
	if ctx == nil {
		return errors.New("perfuncted: stop application: nil context")
	}
	select {
	case <-a.done:
		return nil
	default:
	}
	if err := signalProcessGroup(a.pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("perfuncted: stop application %d: %w", a.pid, err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		return nil
	}
}

// Kill sends SIGKILL to the application process group.
func (a *Application) Kill() error {
	if a == nil || a.pid <= 0 {
		return nil
	}
	select {
	case <-a.done:
		return nil
	default:
	}
	if err := signalProcessGroup(a.pid, syscall.SIGKILL); err != nil {
		return fmt.Errorf("perfuncted: kill application %d: %w", a.pid, err)
	}
	return nil
}

func signalProcessGroup(pgid int, signal syscall.Signal) error {
	err := syscall.Kill(-pgid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
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
	if a == nil || pid <= 0 {
		return false
	}
	pgid, err := syscall.Getpgid(int(pid))
	return err == nil && pgid == a.pid
}

// LogPath returns the log directory used by owned session infrastructure.
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
