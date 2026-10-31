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
	xdgDir          string
	wlDisplay       string
	dbusAddr        string
	logDir          string
	swayCmd         *managedSessionProcess
	dbusCmd         *managedSessionProcess
	atspiCmd        *managedSessionProcess
	atspiAddr       string
	wlPasteCmd      *managedSessionProcess
	ctx             context.Context //nolint:containedctx // infrastructure owns this context
	cancel          context.CancelFunc
	mu              sync.Mutex
	stopped         bool
	unregister      func()
	stopOnce        sync.Once
	stopDone        chan struct{}
	stopErr         error // published by closing stopDone after teardown
	timeouts        TimeoutPolicy
	startupObserver StartupPhaseObserver
}

func (s *Session) startSession(
	ctx context.Context,
	mode sessionMode,
	config SessionConfig,
	wantAccessibility bool,
) (_ *sessionInfra, startupErr error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("session: startup: %w", err)
	}
	if config.Resolution == (image.Point{}) {
		config.Resolution = image.Pt(1024, 768)
	}
	config.Timeouts = config.Timeouts.WithDefaults()
	startupCtx, startupCancel := context.WithTimeout(ctx, config.Timeouts.Startup)
	defer startupCancel()

	var xdgDir string
	var logDir string
	var infra *sessionInfra
	// Startup owns every created runtime until it returns success. One deferred
	// cleanup handles failures at all stages and reports teardown errors instead
	// of silently leaking an incompletely initialized session.
	defer func() {
		if startupErr == nil {
			return
		}
		if infra != nil {
			if cleanupErr := infra.stop(); cleanupErr != nil {
				startupErr = errors.Join(startupErr, cleanupErr)
			}
		} else if xdgDir != "" {
			if cleanupErr := os.RemoveAll(xdgDir); cleanupErr != nil {
				startupErr = errors.Join(startupErr, fmt.Errorf("session: remove managed runtime after startup failure: %w", cleanupErr))
			}
		}
	}()
	err := s.runStartupPhase(startupCtx, StartupPhaseRuntimeDirectoryPreparation, func() error {
		var prepareErr error
		xdgDir, prepareErr = createManagedSessionRuntimeDir()
		if prepareErr != nil {
			return fmt.Errorf("session: managed runtime: %w", prepareErr)
		}
		logDir, prepareErr = createSessionLogDir(config.LogDir)
		if prepareErr != nil {
			return fmt.Errorf("session: create logs: %w", prepareErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	infraCtx, cancel := context.WithCancel(context.Background())
	infra = &sessionInfra{
		xdgDir:          xdgDir,
		wlDisplay:       "wayland-1",
		dbusAddr:        fmt.Sprintf("unix:path=%s/bus", xdgDir),
		logDir:          logDir,
		ctx:             infraCtx,
		cancel:          cancel,
		stopDone:        make(chan struct{}),
		timeouts:        config.Timeouts,
		startupObserver: s.startupObserver,
	}

	pidPath := filepath.Join(xdgDir, sessionOwnerPIDFile)
	if writeErr := os.WriteFile(
		pidPath,
		[]byte(strconv.Itoa(os.Getpid())),
		0644,
	); writeErr != nil {
		return nil, fmt.Errorf("session: write owner pidfile: %w", writeErr)
	}

	infra.unregister = infra.CleanupOnSignal( //nolint:contextcheck // infrastructure owns this lifecycle
		infra.ctx,
	)

	if launchErr := s.runStartupPhase(startupCtx, StartupPhaseDBusStartup, func() error {
		return infra.launchDBus(startupCtx)
	}); launchErr != nil {
		return nil, fmt.Errorf("session: dbus: %w", launchErr)
	}
	if wantAccessibility {
		// The launcher is optional on minimal CI images. OpenRuntime still
		// reports a typed unavailable capability when no AT-SPI service exists.
		if launchErr := s.runStartupPhase(startupCtx, StartupPhaseAccessibilityLauncher, infra.launchAccessibility); launchErr == nil {
			// The launcher registers org.a11y.Bus asynchronously. Resolve its
			// address under a bounded startup wait before publishing the
			// managed runtime to child applications, so they and OpenRuntime use
			// the same AT-SPI bus. Use Medium rather than Short so a loaded CI
			// host does not leave the managed bus unpublished while still
			// bounded by the overall startup deadline.
			addressCtx, addressCancel := context.WithTimeout(startupCtx, config.Timeouts.Medium)
			var address string
			addressErr := s.runStartupPhase(addressCtx, StartupPhaseAccessibilityBusAddress, func() error {
				var addressErr error
				address, addressErr = infra.accessibilityBusAddress(addressCtx)
				return addressErr
			})
			addressCancel()
			if addressErr == nil {
				infra.atspiAddr = address
			} else {
				slog.Debug("session: accessibility bus address not ready", "error", addressErr)
			}
		}
	}

	var swayConf string
	err = s.runStartupPhase(startupCtx, StartupPhaseSwayConfiguration, func() error {
		swayConf = config.SwayConfigPath
		if swayConf == "" {
			swayConf, err = infra.resolveSwayConfig(mode, config.Resolution)
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("session: sway config: %w", err)
	}

	if err := s.runStartupPhase(startupCtx, StartupPhaseSwayStartup, func() error {
		return infra.launchSway(startupCtx, swayConf, mode)
	}); err != nil {
		return nil, fmt.Errorf("session: sway: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("session: startup: %w", err)
	}

	_ = s.runStartupPhase(startupCtx, StartupPhaseClipboardHelper, infra.launchWlPaste)

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

// maxManagedSessionSocketPathBytes reserves space beyond the Linux sockaddr_un
// pathname boundary (107 bytes). On real systems dbus-daemon can fail to
// publish sockets at nominally valid pathname lengths: its address includes
// "unix:path=" and startup helpers can apply stricter limits. Keep every
// managed socket under this conservative, byte-counted budget. This is for
// managed *runtime* sockets, not arbitrary application Unix sockets.
const maxManagedSessionSocketPathBytes = 80

// managedSessionTempRoots preserves the caller-selected temporary directory
// when safe, but can fall back to /tmp for Unix socket paths that are too long.
// The fallback must also be considered by stale-session cleanup.
func managedSessionTempRoots() []string {
	preferred := filepath.Clean(os.TempDir())
	const shortRoot = "/tmp"
	if preferred == shortRoot {
		return []string{preferred}
	}
	return []string{preferred, shortRoot}
}

func validateManagedSessionSocketPath(path string) error {
	if err := validateSessionUnixSocketPath(path); err != nil {
		return err
	}
	if n := len([]byte(path)); n > maxManagedSessionSocketPathBytes {
		return fmt.Errorf("%w: managed session socket %d bytes, conservative budget %d", ErrSessionSocketPathTooLong, n, maxManagedSessionSocketPathBytes)
	}
	return nil
}

// createManagedSessionRuntimeDir creates a private directory with bounded D-Bus
// and Wayland socket names. If TMPDIR would create unusable sockets, retry
// automatically using a short path; do not ask callers to fix their TMPDIR.
// Random 0700 directories under sticky /tmp preserve session isolation.
func createManagedSessionRuntimeDir() (string, error) {
	var pathErr error
	for _, root := range managedSessionTempRoots() {
		dir, err := os.MkdirTemp(root, "perfuncted-xdg-")
		if err != nil {
			return "", fmt.Errorf("create managed runtime in %q: %w", root, err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("secure managed runtime: %w", err)
		}
		var invalid error
		for _, name := range []string{"bus", "wayland-1"} {
			if err := validateManagedSessionSocketPath(filepath.Join(dir, name)); err != nil {
				invalid = err
				break
			}
		}
		if invalid == nil {
			return dir, nil
		}
		_ = os.RemoveAll(dir)
		pathErr = invalid
	}
	return "", pathErr
}

// ErrSessionSocketPathTooLong reports that a managed session socket pathname
// exceeds the operating system's UNIX-domain socket limit.
var ErrSessionSocketPathTooLong = errors.New("session: socket pathname exceeds UNIX limit")

func validateSessionUnixSocketPath(path string) error {
	maxBytes := len(syscall.RawSockaddrUnix{}.Path) - 1
	size := len([]byte(path))
	if size > maxBytes {
		return fmt.Errorf("%w: %d bytes, max %d bytes", ErrSessionSocketPathTooLong, size, maxBytes)
	}
	return nil
}

func (i *sessionInfra) launchDBus(ctx context.Context) error {
	if !strings.HasPrefix(i.dbusAddr, "unix:path=") {
		return errors.New("session: invalid D-Bus socket address")
	}
	if err := validateManagedSessionSocketPath(strings.TrimPrefix(i.dbusAddr, "unix:path=")); err != nil {
		return err
	}
	cmd := executil.CommandContext(i.ctx, "dbus-daemon", "--session", //nolint:contextcheck // process lifetime outlives startup context
		"--address="+i.dbusAddr,
		"--nofork", "--nopidfile")
	cmd.Env = env.Current().
		WithSession(i.xdgDir, i.wlDisplay, i.dbusAddr).
		Without("WAYLAND_DISPLAY").
		EnvList()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	observeStartupPhase(i.startupObserver, StartupPhaseDBusProcessSpawn, StartupPhaseStarted)
	if err := cmd.Start(); err != nil {
		observeStartupPhase(i.startupObserver, StartupPhaseDBusProcessSpawn, startupPhaseResult(ctx, err))
		return err
	}
	i.dbusCmd = newManagedSessionProcess(cmd)
	i.writeChildPID("dbus.pid", cmd.Process.Pid)
	observeStartupPhase(i.startupObserver, StartupPhaseDBusProcessSpawn, StartupPhaseSucceeded)

	busPath := filepath.Join(i.xdgDir, "bus")
	observeStartupPhase(i.startupObserver, StartupPhaseDBusSocketReady, StartupPhaseStarted)
	if err := waitForFile(ctx, busPath, startupWaitAttempts(i.timeouts), i.timeouts.Poll); err != nil {
		observeStartupPhase(i.startupObserver, StartupPhaseDBusSocketReady, startupPhaseResult(ctx, err))
		return fmt.Errorf("dbus socket %s did not appear within %s: %w", busPath, i.timeouts.Startup, err)
	}
	observeStartupPhase(i.startupObserver, StartupPhaseDBusSocketReady, StartupPhaseSucceeded)
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

	observeStartupPhase(i.startupObserver, StartupPhaseSwayProcessSpawn, StartupPhaseStarted)
	err = cmd.Start()
	if err != nil {
		observeStartupPhase(i.startupObserver, StartupPhaseSwayProcessSpawn, startupPhaseResult(ctx, err))
		logFileClose(logFile)
		return err
	}
	i.swayCmd = newManagedSessionProcess(cmd)
	i.writeChildPID("sway.pid", cmd.Process.Pid)
	logFileClose(logFile)
	observeStartupPhase(i.startupObserver, StartupPhaseSwayProcessSpawn, StartupPhaseSucceeded)

	socketPath := filepath.Join(i.xdgDir, i.wlDisplay)
	ipcGlob := filepath.Join(i.xdgDir, "sway-ipc.*.sock")
	observeStartupPhase(i.startupObserver, StartupPhaseSwaySocketsReady, StartupPhaseStarted)
	g := new(errgroup.Group)
	g.Go(func() error {
		if readinessErr := waitForFile(ctx, socketPath, startupWaitAttempts(i.timeouts), i.timeouts.Poll); readinessErr != nil {
			return fmt.Errorf("wayland socket %s did not appear within %s: %w", socketPath, i.timeouts.Startup, readinessErr)
		}
		return nil
	})
	g.Go(func() error {
		if readinessErr := waitForGlob(ctx, ipcGlob, startupWaitAttempts(i.timeouts), i.timeouts.Poll); readinessErr != nil {
			return fmt.Errorf("sway IPC socket in %s did not appear within %s: %w", i.xdgDir, i.timeouts.Startup, readinessErr)
		}
		return nil
	})
	err = g.Wait()
	observeStartupPhase(i.startupObserver, StartupPhaseSwaySocketsReady, startupPhaseResult(ctx, err))
	return err
}

func (i *sessionInfra) launchWlPaste() error {
	cmd := executil.CommandContext(i.ctx, "wl-paste", "--watch", "cat")
	cmd.Env = env.Environ(i.xdgDir, i.wlDisplay, i.dbusAddr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err := cmd.Start()
	if err == nil {
		i.wlPasteCmd = newManagedSessionProcess(cmd)
		i.writeChildPID("wl-paste.pid", cmd.Process.Pid)
		return nil
	}
	slog.Warn("wl-paste helper failed to start", "error", err)
	return err
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
			if err := i.stop(); err != nil {
				slog.Warn("session: canceled session cleanup failed", "error", err)
			}
		case <-sigs:
			if err := i.stop(); err != nil {
				slog.Warn("session: signal-triggered cleanup failed", "error", err)
			}
		case <-stopCh:
		}
	}()
	return sync.OnceFunc(func() {
		close(stopCh)
	})
}

func (i *sessionInfra) stop() error {
	if i == nil {
		return nil
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
				i.stopErr = fmt.Errorf("session: refuse unsafe managed runtime directory %q", i.xdgDir)
				slog.Warn("session: skip removal of non-managed directory", "path", i.xdgDir)
			} else {
				unmountCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
				unmountErr := unmountSubdirsContext(unmountCtx, i.xdgDir)
				cancel()
				if unmountErr != nil {
					// Leave the directory intact for a later stale-session cleanup.
					// Never traverse a filesystem that may still be mounted.
					i.stopErr = fmt.Errorf("session: unmount managed runtime: %w", unmountErr)
					slog.Warn("session: managed runtime unmount incomplete; retain directory", "error", unmountErr)
				} else if err := os.RemoveAll(i.xdgDir); err != nil {
					i.stopErr = fmt.Errorf("session: remove managed runtime: %w", err)
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
	return i.stopErr
}

func (i *sessionInfra) stopManagedProcess(proc *managedSessionProcess, waitTimeout time.Duration) {
	if proc == nil {
		return
	}
	proc.stop(waitTimeout)
}
