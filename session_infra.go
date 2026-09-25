package perfuncted

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sync/errgroup"

	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
)

//go:embed configs/headless.conf configs/nested.conf
var embeddedConfigs embed.FS

const (
	atspiBusLauncherName  = "at-spi-bus-launcher"
	atspiBusService       = "org.a11y.Bus"
	atspiBusPath          = dbus.ObjectPath("/org/a11y/bus")
	atspiBusAddressMethod = "org.a11y.Bus.GetAddress"
)

type sessionMode int

const (
	sessionModeHeadless sessionMode = iota
	sessionModeNested
)

// sessionInfra owns the runtime directory and child processes for a managed
// target. Startup failures, signals, and Session.Close converge on stop.
type sessionInfra struct {
	xdgDir     string
	wlDisplay  string
	dbusAddr   string
	logDir     string
	swayCmd    *managedSessionProcess
	dbusCmd    *managedSessionProcess
	atspiCmd   *managedSessionProcess
	atspiAddr  string
	wlPasteCmd *managedSessionProcess
	ctx        context.Context //nolint:containedctx // infrastructure owns this context
	cancel     context.CancelFunc
	mu         sync.Mutex
	stopped    bool
	unregister func()
	stopOnce   sync.Once
	stopDone   chan struct{}
	timeouts   TimeoutPolicy
}

func (s *Session) startSession(
	ctx context.Context,
	mode sessionMode,
	config SessionConfig,
	wantAccessibility bool,
) (*sessionInfra, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("session: startup: %w", err)
	}
	if config.Resolution == (image.Point{}) {
		config.Resolution = image.Pt(1024, 768)
	}
	config.Timeouts = config.Timeouts.WithDefaults()
	startupCtx, startupCancel := context.WithTimeout(ctx, config.Timeouts.Startup)
	defer startupCancel()

	xdgDir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		return nil, fmt.Errorf("session: mkdirtemp: %w", err)
	}
	if chmodErr := os.Chmod(xdgDir, 0700); chmodErr != nil {
		os.RemoveAll(xdgDir)
		return nil, fmt.Errorf("session: chmod: %w", chmodErr)
	}

	logDir, logErr := createSessionLogDir(config.LogDir)
	if logErr != nil {
		os.RemoveAll(xdgDir)
		return nil, fmt.Errorf("session: create logs: %w", logErr)
	}

	infraCtx, cancel := context.WithCancel(context.Background())
	infra := &sessionInfra{
		xdgDir:    xdgDir,
		wlDisplay: "wayland-1",
		dbusAddr:  fmt.Sprintf("unix:path=%s/bus", xdgDir),
		logDir:    logDir,
		ctx:       infraCtx,
		cancel:    cancel,
		stopDone:  make(chan struct{}),
		timeouts:  config.Timeouts,
	}

	pidPath := filepath.Join(xdgDir, sessionOwnerPIDFile)
	if writeErr := os.WriteFile(
		pidPath,
		[]byte(strconv.Itoa(os.Getpid())),
		0644,
	); writeErr != nil {
		infra.stop()
		return nil, fmt.Errorf("session: write owner pidfile: %w", writeErr)
	}

	infra.unregister = infra.CleanupOnSignal( //nolint:contextcheck // infrastructure owns this lifecycle
		infra.ctx,
	)

	if launchErr := infra.launchDBus(startupCtx); launchErr != nil {
		infra.stop()
		return nil, fmt.Errorf("session: dbus: %w", launchErr)
	}
	if wantAccessibility {
		// The launcher is optional on minimal CI images. OpenRuntime still
		// reports a typed unavailable capability when no AT-SPI service exists.
		if launchErr := infra.launchAccessibility(); launchErr == nil {
			// The launcher registers org.a11y.Bus asynchronously. Resolve its
			// address under a bounded startup wait before publishing the
			// managed runtime to child applications, so they and OpenRuntime use
			// the same AT-SPI bus. Use Medium rather than Short so a loaded CI
			// host does not leave the managed bus unpublished while still
			// bounded by the overall startup deadline.
			addressCtx, addressCancel := context.WithTimeout(startupCtx, config.Timeouts.Medium)
			address, addressErr := infra.accessibilityBusAddress(addressCtx)
			addressCancel()
			if addressErr == nil {
				infra.atspiAddr = address
			} else {
				slog.Debug("session: accessibility bus address not ready", "error", addressErr)
			}
		}
	}

	swayConf := config.SwayConfigPath
	if swayConf == "" {
		swayConf, err = infra.resolveSwayConfig(mode, config.Resolution)
	}
	if err != nil {
		infra.stop()
		return nil, fmt.Errorf("session: sway config: %w", err)
	}

	if err := infra.launchSway(startupCtx, swayConf, mode); err != nil {
		infra.stop()
		return nil, fmt.Errorf("session: sway: %w", err)
	}
	if err := ctx.Err(); err != nil {
		infra.stop()
		return nil, fmt.Errorf("session: startup: %w", err)
	}

	infra.launchWlPaste()

	return infra, nil
}

func (i *sessionInfra) resolveSwayConfig(mode sessionMode, res image.Point) (string, error) {
	switch mode {
	case sessionModeHeadless:
		return i.writeEmbeddedConfig("configs/headless.conf", res)
	case sessionModeNested:
		return i.writeEmbeddedConfig("configs/nested.conf", image.Point{})
	}
	return "", fmt.Errorf("session: unknown mode %d", mode)
}

func (i *sessionInfra) launchDBus(ctx context.Context) error {
	cmd := executil.CommandContext(i.ctx, "dbus-daemon", "--session", //nolint:contextcheck // process lifetime outlives startup context
		"--address="+i.dbusAddr,
		"--nofork", "--nopidfile")
	cmd.Env = env.Current().
		WithSession(i.xdgDir, i.wlDisplay, i.dbusAddr).
		Without("WAYLAND_DISPLAY").
		EnvList()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	i.dbusCmd = newManagedSessionProcess(cmd)
	i.writeChildPID("dbus.pid", cmd.Process.Pid)

	busPath := filepath.Join(i.xdgDir, "bus")
	if err := waitForFile(ctx, busPath, startupWaitAttempts(i.timeouts), i.timeouts.Poll); err != nil {
		return fmt.Errorf("dbus socket %s did not appear within %s: %w", busPath, i.timeouts.Startup, err)
	}
	return nil
}

// launchAccessibility starts the desktop accessibility bus when the helper
// is installed. AT-SPI is commonly activated on demand, so absence of the
// helper is deliberately non-fatal for optional capability users.
func (i *sessionInfra) launchAccessibility() error {
	launcher, err := exec.LookPath(atspiBusLauncherName)
	if err != nil {
		// Debian/Ubuntu install the helper in /usr/libexec without adding that
		// directory to PATH. Keep the normal PATH lookup first for portable
		// installations, then use the distro-standard location.
		const libexecLauncher = "/usr/libexec/at-spi-bus-launcher"
		if _, statErr := os.Stat(libexecLauncher); statErr != nil {
			return err
		}
		launcher = libexecLauncher
	}
	cmd := executil.CommandContext(i.ctx, launcher, "--launch-immediately") //nolint:contextcheck // process lifetime follows managed session
	cmd.Env = env.Current().WithSession(i.xdgDir, i.wlDisplay, i.dbusAddr).EnvList()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	i.atspiCmd = newManagedSessionProcess(cmd)
	i.writeChildPID("at-spi.pid", cmd.Process.Pid)
	return nil
}

func (i *sessionInfra) accessibilityBusAddress(ctx context.Context) (string, error) {
	policy := i.timeouts.WithDefaults()
	ticker := time.NewTicker(policy.Poll)
	defer ticker.Stop()
	var lastErr error
	for {
		conn, err := dbusutil.SessionBusAddressContext(ctx, i.dbusAddr)
		if err == nil {
			var address string
			err = conn.Object(atspiBusService, atspiBusPath).CallWithContext(ctx, atspiBusAddressMethod, 0).Store(&address)
			_ = conn.Close()
			if err == nil {
				address = strings.TrimSpace(address)
				if address != "" {
					return address, nil
				}
				err = errors.New("empty AT-SPI bus address")
			}
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "", ctx.Err()
			}
			return "", lastErr
		case <-ticker.C:
		}
	}
}

func (i *sessionInfra) launchSway(
	ctx context.Context,
	confPath string,
	mode sessionMode,
) error {
	logPath := filepath.Join(i.logDir, "sway-session.log")
	logFile, err := openPrivateSessionLog(logPath)
	if err != nil {
		return fmt.Errorf("create log: %w", err)
	}

	cmd := executil.CommandContext(i.ctx, "sway", "--unsupported-gpu", "-c", confPath) //nolint:contextcheck // process lifetime outlives startup context
	runtime := env.Current().WithSession(i.xdgDir, "", i.dbusAddr)
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
			logFileClose(logFile)
			return fmt.Errorf("nested session requires a host Wayland socket")
		}
		runtime = runtime.With("WAYLAND_DISPLAY", hostSocket)
		cmd.Env = env.Merge(runtime.EnvList(),
			"WLR_BACKENDS=wayland",
			"WLR_RENDERER=pixman",
		)
	default:
		logFileClose(logFile)
		return fmt.Errorf("unknown session mode %d", mode)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		logFileClose(logFile)
		return err
	}
	i.swayCmd = newManagedSessionProcess(cmd)
	i.writeChildPID("sway.pid", cmd.Process.Pid)
	logFileClose(logFile)

	socketPath := filepath.Join(i.xdgDir, i.wlDisplay)
	ipcGlob := filepath.Join(i.xdgDir, "sway-ipc.*.sock")
	g := new(errgroup.Group)
	g.Go(func() error {
		if err := waitForFile(ctx, socketPath, startupWaitAttempts(i.timeouts), i.timeouts.Poll); err != nil {
			return fmt.Errorf("wayland socket %s did not appear within %s: %w", socketPath, i.timeouts.Startup, err)
		}
		return nil
	})
	g.Go(func() error {
		if err := waitForGlob(ctx, ipcGlob, startupWaitAttempts(i.timeouts), i.timeouts.Poll); err != nil {
			return fmt.Errorf("sway IPC socket in %s did not appear within %s: %w", i.xdgDir, i.timeouts.Startup, err)
		}
		return nil
	})
	return g.Wait()
}

func (i *sessionInfra) launchWlPaste() {
	cmd := executil.CommandContext(i.ctx, "wl-paste", "--watch", "cat")
	cmd.Env = env.Environ(i.xdgDir, i.wlDisplay, i.dbusAddr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err := cmd.Start()
	if err == nil {
		i.wlPasteCmd = newManagedSessionProcess(cmd)
		i.writeChildPID("wl-paste.pid", cmd.Process.Pid)
		return
	}
	slog.Warn("wl-paste helper failed to start", "error", err)
}

func (i *sessionInfra) writeChildPID(name string, pid int) {
	if i == nil || i.xdgDir == "" || pid <= 0 {
		return
	}
	if !isSafeToRemoveDir(i.xdgDir) {
		slog.Warn("session: skip writing pidfile to non-managed directory", "path", i.xdgDir, "name", name)
		return
	}
	if err := os.WriteFile(filepath.Join(i.xdgDir, name), []byte(strconv.Itoa(pid)), 0o600); err != nil {
		slog.Warn("failed to write child pidfile", "name", name, "pid", pid, "error", err)
	}
}

func (i *sessionInfra) writeEmbeddedConfig(name string, res image.Point) (string, error) {
	data, err := embeddedConfigs.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("read embedded config: %w", err)
	}

	conf := string(data)
	if res.X > 0 && res.Y > 0 {
		resStr := strconv.Itoa(res.X) + "x" + strconv.Itoa(res.Y)
		conf = strings.ReplaceAll(conf, "1024x768", resStr)
	}

	if !isSafeToRemoveDir(i.xdgDir) {
		return "", fmt.Errorf("session: refuse to write config to non-managed directory %q", i.xdgDir)
	}
	confPath := filepath.Join(i.xdgDir, "sway.conf")
	if err := os.WriteFile(confPath, []byte(conf), 0644); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	return confPath, nil
}

// CleanupOnSignal stops the session when ctx is cancelled or when the process
// receives an interrupt/termination signal.
func (i *sessionInfra) CleanupOnSignal(ctx context.Context) func() {
	if i == nil {
		return func() {}
	}
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
			i.stop()
		case <-sigs:
			i.stop()
		case <-stopCh:
		}
	}()
	return sync.OnceFunc(func() {
		close(stopCh)
	})
}

func (i *sessionInfra) stop() {
	if i == nil {
		return
	}
	// Lazily create the join channel so zero-value infra structs (tests,
	// early startup failures) are safe too.
	i.mu.Lock()
	if i.stopDone == nil {
		i.stopDone = make(chan struct{})
	}
	i.mu.Unlock()

	i.stopOnce.Do(func() {
		defer close(i.stopDone)

		i.mu.Lock()
		i.stopped = true
		unregister := i.unregister
		i.unregister = nil
		i.mu.Unlock()

		if unregister != nil {
			unregister()
		}

		if i.cancel != nil {
			i.cancel()
		}

		stopTimeout := i.timeouts.WithDefaults().Short
		i.stopManagedProcess(i.wlPasteCmd, stopTimeout)
		i.stopManagedProcess(i.swayCmd, stopTimeout)
		i.stopManagedProcess(i.atspiCmd, stopTimeout)
		i.stopManagedProcess(i.dbusCmd, stopTimeout)
		if i.xdgDir != "" {
			if !isSafeToRemoveDir(i.xdgDir) {
				slog.Warn("session: skip removal of non-managed directory", "path", i.xdgDir)
			} else {
				unmountSubdirs(i.xdgDir)
				if err := os.RemoveAll(i.xdgDir); err != nil {
					slog.Debug("session: remove xdg dir", "path", i.xdgDir, "error", err)
				}
			}
		}
	})
	// Join with the teardown sequence. Without this, a second caller (for
	// example Session.Close after the auto-registered signal handler already
	// entered stop) would return immediately and the process could exit while
	// the first teardown is still killing sway/dbus or unmounting the XDG
	// dir, orphaning those processes and mounts.
	<-i.stopDone
}

func (i *sessionInfra) stopManagedProcess(proc *managedSessionProcess, waitTimeout time.Duration) {
	if proc == nil {
		return
	}
	proc.stop(waitTimeout)
}
