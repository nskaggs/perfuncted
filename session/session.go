// Package session manages headless sway sessions for desktop automation.
//
// A Session encapsulates the full lifecycle of an isolated Wayland session:
// a temporary XDG_RUNTIME_DIR, dbus-daemon, headless sway compositor, and
// wl-paste clipboard watcher. Callers use it to automate GUI applications
// without touching the host desktop.
//
// Quick start:
//
//	sess, err := session.Start(session.Config{})
//	if err != nil { log.Fatal(err) }
//	defer sess.Stop()
//
//	pf, err := sess.Perfuncted(perfuncted.Options{})
//	cmd, _ := sess.Launch("kwrite", "/tmp/test.txt")
package session

import (
	"context"
	"embed"
	"fmt"
	"image"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
)

//go:embed configs/ci.conf configs/headless.conf
var embeddedConfigs embed.FS

// Config controls session creation.
type Config struct {
	// Resolution sets the headless output size. Zero value defaults to 1024x768.
	Resolution image.Point

	// SwayConfigPath overrides the embedded sway config. When empty, the
	// embedded ci.conf is written to the temp dir and used.
	SwayConfigPath string

	// LogDir is the directory for sway log output. Defaults to /tmp/perfuncted-logs.
	LogDir string
}

// Session is a running headless sway session.
type Session struct {
	xdgDir     string
	wlDisplay  string
	dbusAddr   string
	swayPid    int
	dbusPid    int
	wlPastePid int
	swayCmd    *exec.Cmd
	dbusCmd    *exec.Cmd
	wlPasteCmd *exec.Cmd
	logDir     string
	mu         sync.Mutex
	stopped    bool
}

// managedProc wraps a started process for unified lifecycle management.
type managedProc struct {
	cmd *exec.Cmd
	pid int
}

// stop terminates the process group using SIGTERM and waits up to the
// provided timeout for the process to exit. If the process doesn't exit
// in time, SIGKILL is used as a fallback.
func (m *managedProc) stop(waitTimeout time.Duration) {
	if m == nil || m.pid <= 0 {
		return
	}
	// First try graceful termination of the process group.
	_ = syscall.Kill(-m.pid, syscall.SIGTERM)
	if m.cmd == nil {
		time.Sleep(waitTimeout)
		return
	}
	// Use cmd.Wait in a goroutine to avoid busy-polling the PID. If Wait
	// returns within the timeout, the process exited cleanly; otherwise
	// send SIGKILL and wait again briefly.
	done := make(chan struct{})
	go func() {
		_ = m.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(waitTimeout):
		// Force kill the process group.
		_ = syscall.Kill(-m.pid, syscall.SIGKILL)
		select {
		case <-done:
			return
		case <-time.After(waitTimeout):
			return
		}
	}
}

// Start creates a new isolated headless sway session. It launches dbus-daemon,
// headless sway, and wl-paste, then waits for the Wayland and sway IPC sockets
// to appear.
func Start(cfg Config) (*Session, error) {
	if cfg.Resolution == (image.Point{}) {
		cfg.Resolution = image.Pt(1024, 768)
	}
	if cfg.LogDir == "" {
		cfg.LogDir = "/tmp/perfuncted-logs"
	}
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		return nil, fmt.Errorf("session: mkdir logs: %w", err)
	}

	xdgDir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		return nil, fmt.Errorf("session: mkdirtemp: %w", err)
	}
	if err := os.Chmod(xdgDir, 0700); err != nil {
		os.RemoveAll(xdgDir)
		return nil, fmt.Errorf("session: chmod: %w", err)
	}

	s := &Session{
		xdgDir:    xdgDir,
		wlDisplay: "wayland-1",
		dbusAddr:  fmt.Sprintf("unix:path=%s/bus", xdgDir),
		logDir:    cfg.LogDir,
	}

	// 1. Launch dbus-daemon.
	if err := s.launchDBus(); err != nil {
		s.Stop()
		return nil, fmt.Errorf("session: dbus: %w", err)
	}

	// 2. Resolve sway config.
	swayConf := cfg.SwayConfigPath
	if swayConf == "" {
		swayConf, err = s.writeEmbeddedConfig(cfg.Resolution)
		if err != nil {
			s.Stop()
			return nil, fmt.Errorf("session: sway config: %w", err)
		}
	}

	// 3. Launch headless sway.
	if err := s.launchSway(swayConf); err != nil {
		s.Stop()
		return nil, fmt.Errorf("session: sway: %w", err)
	}

	// 4. Launch wl-paste --watch for clipboard support.
	s.launchWlPaste()

	return s, nil
}

// Perfuncted returns a connected perfuncted instance targeting this session.
// The returned instance should be closed separately from the session.
func (s *Session) Perfuncted(opts perfuncted.Options) (*perfuncted.Perfuncted, error) {
	opts.XDGRuntimeDir = s.xdgDir
	opts.WaylandDisplay = s.wlDisplay
	opts.DBusSessionAddress = s.dbusAddr
	return perfuncted.New(opts)
}

// Launch starts a subprocess inside the session with the correct environment.
// The caller is responsible for waiting on or killing the returned Cmd.
func (s *Session) Launch(name string, args ...string) (*exec.Cmd, error) {
	path, err := executil.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("session: %s not found: %w", name, err)
	}
	cmd := executil.CommandContext(context.Background(), path, args...)
	cmd.Env = s.Env()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("session: start %s: %w", name, err)
	}
	return cmd, nil
}

// Env returns a complete environment variable slice for child processes
// running inside this session. It overlays session vars on the host env.
func (s *Session) Env() []string {
	return env.Environ(s.xdgDir, s.wlDisplay, s.dbusAddr)
}

// XDGRuntimeDir returns the temporary directory path for this session.
func (s *Session) XDGRuntimeDir() string { return s.xdgDir }

// WaylandDisplay returns the Wayland display name (e.g. "wayland-1").
func (s *Session) WaylandDisplay() string { return s.wlDisplay }

// DBusAddress returns the D-Bus session bus address.
func (s *Session) DBusAddress() string { return s.dbusAddr }

// CleanupOnSignal stops the session when ctx is cancelled or when the process
// receives an interrupt/termination signal. It returns a function that
// unregisters the handler without stopping the session.
//
// SIGKILL and hard crashes cannot be handled in-process; callers should still
// keep an external cleanup path for those cases.
func (s *Session) CleanupOnSignal(ctx context.Context) func() {
	if ctx == nil {
		ctx = context.Background()
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	stopCh := make(chan struct{})
	go func() {
		defer signal.Stop(sigs)
		select {
		case <-ctx.Done():
			s.Stop()
		case <-sigs:
			s.Stop()
		case <-stopCh:
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stopCh)
		})
	}
}

// Stop tears down the session in reverse order: wl-paste, sway, dbus,
// then removes the temporary XDG directory.
func (s *Session) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()

	s.stopManagedProcess(s.wlPasteCmd, s.wlPastePid, 200*time.Millisecond)
	s.stopManagedProcess(s.swayCmd, s.swayPid, 500*time.Millisecond)
	s.stopManagedProcess(s.dbusCmd, s.dbusPid, 200*time.Millisecond)
	if s.xdgDir != "" {
		os.RemoveAll(s.xdgDir)
	}
}

// IsStopped returns true if Stop has been called.
func (s *Session) IsStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// Environ is a thin wrapper around internal/env.Environ for callers/tests that
// relied on the original package-level helper.
func Environ(xdgRuntimeDir, waylandDisplay, dbusAddr string) []string {
	return env.Environ(xdgRuntimeDir, waylandDisplay, dbusAddr)
}

func (s *Session) launchDBus() error {
	cmd := executil.CommandContext(context.Background(), "dbus-daemon", "--session",
		"--address="+s.dbusAddr,
		"--nofork", "--nopidfile")
	cmd.Env = env.Merge(os.Environ(), "XDG_RUNTIME_DIR="+s.xdgDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	s.dbusPid = cmd.Process.Pid
	s.dbusCmd = cmd

	// Wait for dbus socket to appear.
	busPath := filepath.Join(s.xdgDir, "bus")
	if err := waitForFile(busPath, 100, 100*time.Millisecond); err != nil {
		return fmt.Errorf("dbus socket %s did not appear within 10s: %w", busPath, err)
	}
	return nil
}

func (s *Session) launchSway(confPath string) error {
	logPath := filepath.Join(s.logDir, "sway-session.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create log: %w", err)
	}

	cmd := executil.CommandContext(context.Background(), "sway", "--unsupported-gpu", "-c", confPath)
	cmd.Env = env.Merge(os.Environ(),
		"WLR_BACKENDS=headless",
		"WLR_RENDERER=pixman",
		"XDG_RUNTIME_DIR="+s.xdgDir,
		"DBUS_SESSION_BUS_ADDRESS="+s.dbusAddr,
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}
	s.swayPid = cmd.Process.Pid
	s.swayCmd = cmd
	logFile.Close()

	// Wait for wayland socket.
	socketPath := filepath.Join(s.xdgDir, s.wlDisplay)
	if err := waitForFile(socketPath, 150, 200*time.Millisecond); err != nil {
		return fmt.Errorf("wayland socket %s did not appear within 30s: %w", socketPath, err)
	}

	// Wait for sway IPC socket as well so callers depending on window control
	// don't race browser startup against compositor readiness.
	if err := waitForGlob(filepath.Join(s.xdgDir, "sway-ipc.*.sock"), 150, 200*time.Millisecond); err != nil {
		return fmt.Errorf("sway IPC socket in %s did not appear within 30s: %w", s.xdgDir, err)
	}
	return nil
}

func (s *Session) launchWlPaste() {
	cmd := executil.CommandContext(context.Background(), "wl-paste", "--watch", "cat")
	cmd.Env = env.Merge(os.Environ(),
		"XDG_RUNTIME_DIR="+s.xdgDir,
		"WAYLAND_DISPLAY="+s.wlDisplay,
		"DBUS_SESSION_BUS_ADDRESS="+s.dbusAddr,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err == nil {
		s.wlPastePid = cmd.Process.Pid
		s.wlPasteCmd = cmd
	}
}

func (s *Session) stopManagedProcess(cmd *exec.Cmd, pid int, waitTimeout time.Duration) {
	(&managedProc{cmd: cmd, pid: pid}).stop(waitTimeout)
}

// waitForFile checks for the existence of the given path up to attempts times,
// sleeping interval between tries.
func waitForFile(path string, attempts int, interval time.Duration) error {
	for i := 0; i < attempts; i++ {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if i == attempts-1 {
			break
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("%s did not appear within %s", path, time.Duration(attempts)*interval)
}

// waitForGlob checks that a glob pattern matches at least one file within the
// given attempts × interval window.
func waitForGlob(pattern string, attempts int, interval time.Duration) error {
	for i := 0; i < attempts; i++ {
		if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
			return nil
		}
		if i == attempts-1 {
			break
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("pattern %s did not match within %s", pattern, time.Duration(attempts)*interval)
}

// writeEmbeddedConfig writes the embedded ci.conf to the temp dir, patching
// the resolution to match the requested config.
func (s *Session) writeEmbeddedConfig(res image.Point) (string, error) {
	data, err := embeddedConfigs.ReadFile("configs/ci.conf")
	if err != nil {
		return "", fmt.Errorf("read embedded config: %w", err)
	}

	// Patch resolution if non-default.
	conf := string(data)
	if res.X > 0 && res.Y > 0 {
		resStr := fmt.Sprintf("%dx%d", res.X, res.Y)
		conf = strings.ReplaceAll(conf, "1024x768", resStr)
	}

	confPath := filepath.Join(s.xdgDir, "sway.conf")
	if err := os.WriteFile(confPath, []byte(conf), 0644); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	return confPath, nil
}
