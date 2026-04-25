package perfuncted

import (
	"context"
	"embed"
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
)

//go:embed configs/ci.conf configs/headless.conf config/sway/nested.conf
var embeddedConfigs embed.FS

// SessionConfig controls session creation.
type SessionConfig struct {
	// Resolution sets the headless output size. Zero value defaults to 1024x768.
	Resolution image.Point

	// SwayConfigPath overrides the embedded sway config. When empty, the
	// embedded ci.conf is written to the temp dir and used.
	SwayConfigPath string

	// LogDir is the directory for sway log output. Defaults to /tmp/perfuncted-logs.
	LogDir string
}

type sessionMode int

const (
	sessionModeHeadless sessionMode = iota
	sessionModeNested
)

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
	outer := time.NewTimer(waitTimeout)
	select {
	case <-done:
		outer.Stop()
		return
	case <-outer.C:
		// Force kill the process group.
		_ = syscall.Kill(-m.pid, syscall.SIGKILL)
		inner := time.NewTimer(waitTimeout)
		select {
		case <-done:
			inner.Stop()
			return
		case <-inner.C:
			return
		}
	}
}

// StartSession creates a new isolated headless sway session. It launches dbus-daemon,
// headless sway, and wl-paste, then waits for the Wayland and sway IPC sockets
// to appear.
func StartSession(cfg SessionConfig) (*Session, error) {
	return startSession(cfg, sessionModeHeadless)
}

// StartNestedSession creates a new isolated nested sway session. It launches
// dbus-daemon, nested sway, and wl-paste, then waits for the Wayland and sway
// IPC sockets to appear.
func StartNestedSession(cfg SessionConfig) (*Session, error) {
	return startSession(cfg, sessionModeNested)
}

func startSession(cfg SessionConfig, mode sessionMode) (*Session, error) {
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
	err = os.Chmod(xdgDir, 0700)
	if err != nil {
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
	err = s.launchDBus()
	if err != nil {
		s.Stop()
		return nil, fmt.Errorf("session: dbus: %w", err)
	}

	// 2. Resolve sway config.
	swayConf := cfg.SwayConfigPath
	if swayConf == "" {
		switch mode {
		case sessionModeHeadless:
			swayConf, err = s.writeEmbeddedConfig("configs/ci.conf", cfg.Resolution)
		case sessionModeNested:
			swayConf, err = s.writeEmbeddedConfig("config/sway/nested.conf", image.Point{})
		default:
			err = fmt.Errorf("session: unknown mode %d", mode)
		}
		if err != nil {
			s.Stop()
			return nil, fmt.Errorf("session: sway config: %w", err)
		}
	}

	// 3. Launch sway.
	if err := s.launchSway(swayConf, mode); err != nil {
		s.Stop()
		return nil, fmt.Errorf("session: sway: %w", err)
	}

	// 4. Launch wl-paste --watch for clipboard support.
	s.launchWlPaste()

	return s, nil
}

// Launch starts a subprocess inside the session with the correct environment.
// The caller is responsible for waiting on or killing the returned Cmd.
func (s *Session) Launch(name string, args ...string) (*exec.Cmd, error) {
	return s.LaunchEnv(nil, name, args...)
}

// LaunchEnv starts a subprocess inside the session with the correct
// environment plus any additional overrides in extraEnv.
func (s *Session) LaunchEnv(extraEnv []string, name string, args ...string) (*exec.Cmd, error) {
	path, err := executil.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("session: %s not found: %w", name, err)
	}
	cmd := executil.CommandContext(context.Background(), path, args...)
	cmd.Env = env.Merge(s.Env(), extraEnv...)
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

// Perfuncted returns a connected perfuncted instance targeting this session.
// The returned instance should be closed separately from the session.
func (s *Session) Perfuncted(opts Options) (*Perfuncted, error) {
	opts.XDGRuntimeDir = s.xdgDir
	opts.WaylandDisplay = s.wlDisplay
	opts.DBusSessionAddress = s.dbusAddr
	return New(opts)
}

// CleanupOnSignal stops the session when ctx is cancelled or when the process
// receives an interrupt/termination signal. It returns a function that
// unregisters the handler without stopping the session.
func (s *Session) CleanupOnSignal(ctx context.Context) func() {
	var done <-chan struct{}
	if ctx != nil {
		done = ctx.Done()
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	stopCh := make(chan struct{})
	go func() {
		defer signal.Stop(sigs)
		select {
		case <-done:
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

func (s *Session) launchDBus() error {
	cmd := executil.CommandContext(context.Background(), "dbus-daemon", "--session",
		"--address="+s.dbusAddr,
		"--nofork", "--nopidfile")
	cmd.Env = env.Current().
		WithSession(s.xdgDir, s.wlDisplay, s.dbusAddr).
		Without("WAYLAND_DISPLAY").
		EnvList()
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

func (s *Session) launchSway(confPath string, mode sessionMode) error {
	logPath := filepath.Join(s.logDir, "sway-session.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create log: %w", err)
	}

	cmd := executil.CommandContext(context.Background(), "sway", "--unsupported-gpu", "-c", confPath)
	// Start the compositor with its target runtime variables, but do not pass
	// SWAYSOCK=. Sway owns this variable and uses it for its IPC socket path.
	runtime := env.Current().
		With("XDG_RUNTIME_DIR", s.xdgDir).
		With("DBUS_SESSION_BUS_ADDRESS", s.dbusAddr).
		Without("SWAYSOCK")
	switch mode {
	case sessionModeHeadless:
		runtime = runtime.Without("WAYLAND_DISPLAY", "DISPLAY")
		cmd.Env = env.Merge(runtime.EnvList(),
			"WLR_BACKENDS=headless",
			"WLR_RENDERER=pixman",
		)
	case sessionModeNested:
		hostSocket := env.Current().SocketPath()
		if hostSocket == "" {
			logFile.Close()
			return fmt.Errorf("nested session requires a host Wayland socket")
		}
		runtime = runtime.With("WAYLAND_DISPLAY", hostSocket)
		cmd.Env = env.Merge(runtime.EnvList(),
			"WLR_BACKENDS=wayland",
			"WLR_RENDERER=pixman",
		)
	default:
		logFile.Close()
		return fmt.Errorf("unknown session mode %d", mode)
	}
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
	cmd.Env = s.Env()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err := cmd.Start()
	if err == nil {
		s.wlPastePid = cmd.Process.Pid
		s.wlPasteCmd = cmd
		return
	}
	// Best-effort background helper failed to start; log so users see the reason.
	log.Printf("warning: wl-paste helper failed to start: %v", err)
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

// writeEmbeddedConfig writes an embedded sway config to the temp dir, patching
// the resolution token when requested.
func (s *Session) writeEmbeddedConfig(name string, res image.Point) (string, error) {
	data, err := embeddedConfigs.ReadFile(name)
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
