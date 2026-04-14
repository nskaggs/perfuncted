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
	"embed"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nskaggs/perfuncted"
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
	logDir     string
	stopped    bool
}

// Start creates a new isolated headless sway session. It launches dbus-daemon,
// headless sway, and wl-paste, then waits for the Wayland socket to appear.
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
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("session: %s not found: %w", name, err)
	}
	cmd := exec.Command(path, args...)
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
	return Environ(s.xdgDir, s.wlDisplay, s.dbusAddr)
}

// XDGRuntimeDir returns the temporary directory path for this session.
func (s *Session) XDGRuntimeDir() string { return s.xdgDir }

// WaylandDisplay returns the Wayland display name (e.g. "wayland-1").
func (s *Session) WaylandDisplay() string { return s.wlDisplay }

// DBusAddress returns the D-Bus session bus address.
func (s *Session) DBusAddress() string { return s.dbusAddr }

// Stop tears down the session in reverse order: wl-paste, sway, dbus,
// then removes the temporary XDG directory.
func (s *Session) Stop() {
	if s.stopped {
		return
	}
	s.stopped = true

	if s.wlPastePid > 0 {
		syscall.Kill(-s.wlPastePid, syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
	}
	if s.swayPid > 0 {
		syscall.Kill(-s.swayPid, syscall.SIGTERM)
		time.Sleep(500 * time.Millisecond)
	}
	if s.dbusPid > 0 {
		syscall.Kill(-s.dbusPid, syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
	}
	if s.xdgDir != "" {
		os.RemoveAll(s.xdgDir)
	}
}

// Environ builds a complete environment variable slice by overlaying session
// variables on the current process environment. Useful for exec.Cmd.Env when
// launching processes into a specific session without constructing a full
// Session object.
func Environ(xdgRuntimeDir, waylandDisplay, dbusAddr string) []string {
	var filtered []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "XDG_RUNTIME_DIR=") ||
			strings.HasPrefix(e, "WAYLAND_DISPLAY=") ||
			strings.HasPrefix(e, "DBUS_SESSION_BUS_ADDRESS=") ||
			strings.HasPrefix(e, "DISPLAY=") {
			continue
		}
		filtered = append(filtered, e)
	}
	filtered = append(filtered,
		"XDG_RUNTIME_DIR="+xdgRuntimeDir,
		"WAYLAND_DISPLAY="+waylandDisplay,
		"DBUS_SESSION_BUS_ADDRESS="+dbusAddr,
		"DISPLAY=",
		"GDK_BACKEND=wayland",
		"QT_QPA_PLATFORM=wayland",
	)
	return filtered
}

func (s *Session) launchDBus() error {
	cmd := exec.Command("dbus-daemon", "--session",
		"--address="+s.dbusAddr,
		"--nofork", "--nopidfile")
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+s.xdgDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	s.dbusPid = cmd.Process.Pid

	// Wait for dbus socket to appear.
	busPath := filepath.Join(s.xdgDir, "bus")
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(busPath); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("dbus socket %s did not appear within 10s", busPath)
}

func (s *Session) launchSway(confPath string) error {
	logPath := filepath.Join(s.logDir, "sway-session.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create log: %w", err)
	}

	cmd := exec.Command("sway", "--unsupported-gpu", "-c", confPath)
	cmd.Env = append(os.Environ(),
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
	logFile.Close()

	// Wait for wayland socket.
	socketPath := filepath.Join(s.xdgDir, s.wlDisplay)
	for i := 0; i < 150; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("wayland socket %s did not appear within 30s", socketPath)
}

func (s *Session) launchWlPaste() {
	cmd := exec.Command("wl-paste", "--watch", "cat")
	cmd.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+s.xdgDir,
		"WAYLAND_DISPLAY="+s.wlDisplay,
		"DBUS_SESSION_BUS_ADDRESS="+s.dbusAddr,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err == nil {
		s.wlPastePid = cmd.Process.Pid
	}
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
