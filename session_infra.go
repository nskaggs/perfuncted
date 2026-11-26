package perfuncted

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
	"github.com/nskaggs/perfuncted/v2/internal/dbusutil"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/internal/executil"
	sessionruntime "github.com/nskaggs/perfuncted/v2/internal/runtime"
	"golang.org/x/sync/errgroup"
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
	swayCmd         *sessionruntime.SessionProcess
	dbusCmd         *sessionruntime.SessionProcess
	atspiCmd        *sessionruntime.SessionProcess
	atspiAddr       string
	wlPasteCmd      *sessionruntime.SessionProcess
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

func (s *sessionCore) startSession(
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
	//nolint:contextcheck // Startup failure teardown must survive cancellation and owns independent deadlines.
	defer func() {
		if startupErr == nil {
			return
		}
		if infra != nil {
			// Startup cancellation must not interrupt teardown of managed
			// processes, sockets, or mounts; stop owns bounded cleanup deadlines.
			if cleanupErr := infra.stop(); cleanupErr != nil { //nolint:contextcheck // Cleanup must outlive the canceled startup context.
				startupErr = errors.Join(startupErr, cleanupErr)
			}
		} else if xdgDir != "" {
			if cleanupErr := os.RemoveAll(xdgDir); cleanupErr != nil {
				startupErr = errors.Join(startupErr, fmt.Errorf("session: remove managed runtime after startup failure: %w", cleanupErr))
			}
		}
	}()
	err := s.runStartupPhase(startupCtx, StartupPhaseRuntimeDirectoryPreparation, func() error {
		return prepareSessionRuntimeDirs(&xdgDir, &logDir, config.LogDir)
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
	// Publish through a new inode: an existing symlink or hard link must
	// never redirect the owner metadata write outside this runtime.
	if writeErr := sessionruntime.WriteManagedSessionFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); writeErr != nil {
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
		s.tryStartAccessibility(startupCtx, infra, config.Timeouts.Medium)
	}
	// Sway's config path is selected under the same bounded startup phase.
	swayConf, err := s.resolveSessionSwayConfig(startupCtx, infra, mode, config)
	if err != nil {
		return nil, err
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

// tryStartAccessibility keeps optional AT-SPI launch and address lookup
// isolated from mandatory managed-desktop startup. Neither failure may
// publish an untrusted address or prevent the selected desktop from starting.
// prepareSessionRuntimeDirs publishes the runtime path immediately after
// creation so startup's deferred cleanup can remove it if log setup fails.
func prepareSessionRuntimeDirs(xdgDir, logDir *string, logPath string) error {
	var err error
	*xdgDir, err = createManagedSessionRuntimeDir()
	if err != nil {
		return fmt.Errorf("session: managed runtime: %w", err)
	}
	*logDir, err = sessionruntime.CreateSessionLogDir(logPath)
	if err != nil {
		return fmt.Errorf("session: create logs: %w", err)
	}
	return nil
}

func (s *sessionCore) tryStartAccessibility(startupCtx context.Context, infra *sessionInfra, wait time.Duration) {
	if launchErr := s.runStartupPhase(startupCtx, StartupPhaseAccessibilityLauncher, infra.launchAccessibility); launchErr != nil {
		return
	}
	// The launcher registers org.a11y.Bus asynchronously; resolve under a
	// bounded wait before advertising the address to managed applications.
	addressCtx, cancel := context.WithTimeout(startupCtx, wait)
	defer cancel()
	var address string
	err := s.runStartupPhase(addressCtx, StartupPhaseAccessibilityBusAddress, func() error {
		var readErr error
		address, readErr = infra.accessibilityBusAddress(addressCtx)
		return readErr
	})
	if err != nil {
		slog.Debug("session: accessibility bus address not ready", "error", err)
		return
	}
	infra.atspiAddr = address
}

func (s *sessionCore) resolveSessionSwayConfig(ctx context.Context, infra *sessionInfra, mode sessionMode, config SessionConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("session: sway config: %w", err)
	}
	swayConf := config.SwayConfigPath
	err := s.runStartupPhase(ctx, StartupPhaseSwayConfiguration, func() error {
		if swayConf != "" {
			return nil
		}
		var resolveErr error
		swayConf, resolveErr = infra.resolveSwayConfig(mode, config.Resolution)
		return resolveErr
	})
	if err != nil {
		return "", fmt.Errorf("session: sway config: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("session: sway config: %w", err)
	}
	return swayConf, nil
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
	for _, root := range sessionruntime.ManagedSessionTempRoots() {
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
	// Linux pathname sockets cannot represent embedded NUL bytes. Reject
	// them before launching a managed session or validating length alone.
	if strings.IndexByte(path, 0) >= 0 {
		return errors.New("session: socket pathname contains NUL byte")
	}
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
	i.dbusCmd = sessionruntime.NewSessionProcess(cmd)
	i.writeChildPID("dbus.pid", i.dbusCmd.PID())
	observeStartupPhase(i.startupObserver, StartupPhaseDBusProcessSpawn, StartupPhaseSucceeded)

	busPath := filepath.Join(i.xdgDir, "bus")
	observeStartupPhase(i.startupObserver, StartupPhaseDBusSocketReady, StartupPhaseStarted)
	if err := sessionruntime.WaitForSocket(ctx, busPath, startupWaitAttempts(i.timeouts), i.timeouts.Poll); err != nil {
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
	launcher, err := findAccessibilityLauncher(filepath.Join("/usr/libexec", atspiBusLauncherName))
	if err != nil {
		return err
	}
	cmd := executil.CommandContext(i.ctx, launcher, "--launch-immediately") //nolint:contextcheck // process lifetime follows managed session
	// The managed session owns a private bus; broker activation through the
	// host user manager cannot start services on that bus.
	cmd.Env = env.Current().WithSession(i.xdgDir, i.wlDisplay, i.dbusAddr).With("ATSPI_DBUS_IMPLEMENTATION", "dbus-daemon").EnvList()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	i.atspiCmd = sessionruntime.NewSessionProcess(cmd)
	i.writeChildPID("at-spi.pid", i.atspiCmd.PID())
	return nil
}

func findAccessibilityLauncher(fallback string) (string, error) {
	launcher, err := exec.LookPath(atspiBusLauncherName)
	if err == nil {
		return launcher, nil
	}
	// Debian/Ubuntu install the helper in /usr/libexec without adding that
	// directory to PATH. Keep the normal PATH lookup first for portable
	// installations, then use the distro-standard location.
	if _, statErr := os.Stat(fallback); statErr != nil {
		return "", err
	}
	return fallback, nil
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
			// A provider error describes the last retry, but the caller's
			// deadline or cancellation is the terminal reason for stopping.
			// Preserve both error identities for diagnostics and errors.Is.
			return "", errors.Join(ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func (i *sessionInfra) launchSway(
	ctx context.Context,
	confPath string,
	mode sessionMode,
) error {
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
			return fmt.Errorf("nested session requires a host Wayland socket")
		}
		runtime = runtime.With("WAYLAND_DISPLAY", hostSocket)
		cmd.Env = env.Merge(runtime.EnvList(),
			"WLR_BACKENDS=wayland",
			"WLR_RENDERER=pixman",
		)
	default:
		return fmt.Errorf("unknown session mode %d", mode)
	}
	// A rejected startup must not create a log file that prevents a later
	// attempt in this session from using the same fresh log directory.
	logPath := filepath.Join(i.logDir, "sway-session.log")
	logFile, err := sessionruntime.OpenPrivateSessionLog(logPath)
	if err != nil {
		return fmt.Errorf("create log: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	observeStartupPhase(i.startupObserver, StartupPhaseSwayProcessSpawn, StartupPhaseStarted)
	err = cmd.Start()
	if err != nil {
		observeStartupPhase(i.startupObserver, StartupPhaseSwayProcessSpawn, startupPhaseResult(ctx, err))
		sessionruntime.CloseLogFile(logFile)
		return err
	}
	i.swayCmd = sessionruntime.NewSessionProcess(cmd)
	i.writeChildPID("sway.pid", i.swayCmd.PID())
	sessionruntime.CloseLogFile(logFile)
	observeStartupPhase(i.startupObserver, StartupPhaseSwayProcessSpawn, StartupPhaseSucceeded)

	socketPath := filepath.Join(i.xdgDir, i.wlDisplay)
	observeStartupPhase(i.startupObserver, StartupPhaseSwaySocketsReady, StartupPhaseStarted)
	g := new(errgroup.Group)
	g.Go(func() error {
		if readinessErr := sessionruntime.WaitForSocket(ctx, socketPath, startupWaitAttempts(i.timeouts), i.timeouts.Poll); readinessErr != nil {
			return fmt.Errorf("wayland socket %s did not appear within %s: %w", socketPath, i.timeouts.Startup, readinessErr)
		}
		return nil
	})
	g.Go(func() error {
		if readinessErr := sessionruntime.WaitForSwayIPCSocket(ctx, i.xdgDir, startupWaitAttempts(i.timeouts), i.timeouts.Poll); readinessErr != nil {
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
		i.wlPasteCmd = sessionruntime.NewSessionProcess(cmd)
		i.writeChildPID("wl-paste.pid", i.wlPasteCmd.PID())
		return nil
	}
	slog.Warn("wl-paste helper failed to start", "error", err)
	return err
}

func (i *sessionInfra) writeChildPID(name string, pid int) {
	if i == nil || i.xdgDir == "" || pid <= 0 {
		return
	}
	// Child PID names are fixed basenames, never caller-controlled paths.
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.IndexByte(name, 0) >= 0 {
		slog.Warn("session: reject unsafe child pid filename", "name", name)
		return
	}
	if !sessionruntime.IsPrivateManagedSessionDir(i.xdgDir) {
		slog.Warn("session: skip writing pidfile to non-managed directory", "path", i.xdgDir, "name", name)
		return
	}
	if err := sessionruntime.WriteManagedSessionFile(filepath.Join(i.xdgDir, name), []byte(strconv.Itoa(pid)), 0o600); err != nil {
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

	if !sessionruntime.IsPrivateManagedSessionDir(i.xdgDir) {
		return "", fmt.Errorf("session: refuse to write config to non-managed directory %q", i.xdgDir)
	}
	confPath := filepath.Join(i.xdgDir, "sway.conf")
	if err := sessionruntime.WriteManagedSessionFile(confPath, []byte(conf), 0o644); err != nil {
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
	//nolint:contextcheck // Signal cleanup cannot inherit an already-canceled caller's context.
	go func() {
		defer signal.Stop(sigs)
		select {
		case <-done:
			// The observed context is already canceled; cleanup must use
			// independent timeouts instead of inheriting that cancellation.
			if err := i.stop(); err != nil { //nolint:contextcheck // Signal teardown has independent cleanup deadlines.
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
			if !sessionruntime.IsSafeToRemoveDir(i.xdgDir) {
				i.stopErr = fmt.Errorf("session: refuse unsafe managed runtime directory %q", i.xdgDir)
				slog.Warn("session: skip removal of non-managed directory", "path", i.xdgDir)
			} else if info, err := os.Lstat(i.xdgDir); errors.Is(err, os.ErrNotExist) {
				// Another cleanup already removed the directory.
			} else if err != nil || !sessionruntime.IsPrivateSessionRuntimeInfo(info) {
				cause := err
				if cause == nil {
					cause = errors.New("runtime directory is not privately owned")
				}
				i.stopErr = fmt.Errorf("session: refuse unsafe managed runtime directory %q: %w", i.xdgDir, cause)
				slog.Warn("session: skip removal of runtime without private ownership", "path", i.xdgDir)
			} else {
				unmountCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
				unmountErr := sessionruntime.UnmountSubdirsContext(unmountCtx, i.xdgDir)
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

func (i *sessionInfra) stopManagedProcess(proc *sessionruntime.SessionProcess, waitTimeout time.Duration) {
	if proc == nil {
		return
	}
	proc.Stop(waitTimeout)
}

// StartupPhase names a bounded boundary inside Open. Values contain no host
// paths, environment values, process identifiers, or application data.
type StartupPhase string

// StartupPhase values identify the component being initialized or observed.
// Each value is safe to expose as bounded diagnostic metadata.
const (
	// StartupPhaseStaleSessionCleanup identifies the managed-session scan before
	// a new managed runtime is opened.
	StartupPhaseStaleSessionCleanup         StartupPhase = "stale_session_cleanup"
	StartupPhaseManagedRuntimeSetup         StartupPhase = "managed_runtime_setup"
	StartupPhaseRuntimeDirectoryPreparation StartupPhase = "runtime_directory_preparation"
	StartupPhaseDBusStartup                 StartupPhase = "dbus_startup"
	StartupPhaseDBusProcessSpawn            StartupPhase = "dbus_process_spawn"
	StartupPhaseDBusSocketReady             StartupPhase = "dbus_socket_ready"
	StartupPhaseAccessibilityLauncher       StartupPhase = "accessibility_launcher"
	StartupPhaseAccessibilityBusAddress     StartupPhase = "accessibility_bus_address"
	StartupPhaseSwayConfiguration           StartupPhase = "sway_configuration"
	StartupPhaseSwayStartup                 StartupPhase = "sway_startup"
	StartupPhaseSwayProcessSpawn            StartupPhase = "sway_process_spawn"
	StartupPhaseSwaySocketsReady            StartupPhase = "sway_sockets_ready"
	StartupPhaseClipboardHelper             StartupPhase = "clipboard_helper"
	StartupPhaseCapabilityScreen            StartupPhase = "capability_screen"
	StartupPhaseCapabilityInput             StartupPhase = "capability_input"
	StartupPhaseCapabilityWindows           StartupPhase = "capability_windows"
	StartupPhaseCapabilityOutputs           StartupPhase = "capability_outputs"
	StartupPhaseCapabilityClipboard         StartupPhase = "capability_clipboard"
	StartupPhaseCapabilityAccessibility     StartupPhase = "capability_accessibility"
)

func capabilityStartupPhase(capability Capability) StartupPhase {
	switch capability {
	case CapabilityScreen:
		return StartupPhaseCapabilityScreen
	case CapabilityInput:
		return StartupPhaseCapabilityInput
	case CapabilityWindows:
		return StartupPhaseCapabilityWindows
	case CapabilityOutputs:
		return StartupPhaseCapabilityOutputs
	case CapabilityClipboard:
		return StartupPhaseCapabilityClipboard
	case CapabilityAccessibility:
		return StartupPhaseCapabilityAccessibility
	default:
		return ""
	}
}

// StartupPhaseState reports only stage start and its terminal result.
type StartupPhaseState string

// StartupPhaseState values distinguish phase entry, success, and failures.
const (
	// StartupPhaseStarted reports that the named Open phase has begun.
	StartupPhaseStarted   StartupPhaseState = "started"
	StartupPhaseSucceeded StartupPhaseState = "succeeded"
	StartupPhaseFailed    StartupPhaseState = "failed"
	StartupPhaseCanceled  StartupPhaseState = "canceled"
	StartupPhaseTimedOut  StartupPhaseState = "timed_out"
)

// StartupPhaseObserver receives fixed phase/state values synchronously. It
// should append bounded data and return without I/O.
type StartupPhaseObserver func(StartupPhase, StartupPhaseState)

func observeStartupPhase(observer StartupPhaseObserver, phase StartupPhase, state StartupPhaseState) {
	if observer == nil {
		return
	}
	defer func() { _ = recover() }()
	observer(phase, state)
}

func startupPhaseResult(ctx context.Context, err error) StartupPhaseState {
	if err == nil {
		return StartupPhaseSucceeded
	}
	if ctx != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return StartupPhaseTimedOut
		}
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			return StartupPhaseCanceled
		}
	}
	return StartupPhaseFailed
}

func (s *sessionCore) runStartupPhase(ctx context.Context, phase StartupPhase, run func() error) error {
	if s == nil || s.startupObserver == nil {
		return run()
	}
	observeStartupPhase(s.startupObserver, phase, StartupPhaseStarted)
	err := run()
	observeStartupPhase(s.startupObserver, phase, startupPhaseResult(ctx, err))
	return err
}

var (
	cleanupStaleSessionsMu sync.Mutex
	lastCleanupTime        time.Time
	cleanupAttempt         uint64
)

const (
	sessionOwnerPIDFile     = sessionruntime.OwnerPIDFile
	cleanupStaleMinInterval = 30 * time.Second
)

// CleanupStaleSessions removes perfuncted session directories immediately when
// their recorded parent PID is no longer running. Missing ownership metadata
// gets a five-minute creation grace; malformed metadata uses maxAge, clamped
// to that same minimum.
func CleanupStaleSessions(maxAge time.Duration) {
	_ = CleanupStaleSessionsContext(context.Background(), maxAge)
}

// CleanupStaleSessionsContext removes stale managed sessions while honoring
// cancellation during process shutdown and mount cleanup. Remaining directories
// are safe to retry on a later cleanup pass.
func CleanupStaleSessionsContext(ctx context.Context, maxAge time.Duration) error {
	if ctx == nil {
		return fmt.Errorf("session cleanup: %w: nil context", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return contextutil.ErrorWithCause(ctx)
	}
	// Claim the interval while locked, then scan outside it so concurrent Opens
	// don't wait for process termination. A canceled newest attempt releases its
	// claim without clearing a newer scan's rate limit.
	cleanupStaleSessionsMu.Lock()
	if time.Since(lastCleanupTime) < cleanupStaleMinInterval {
		cleanupStaleSessionsMu.Unlock()
		if err := ctx.Err(); err != nil {
			return contextutil.ErrorWithCause(ctx)
		}
		return nil
	}
	cleanupAttempt++
	attempt := cleanupAttempt
	lastCleanupTime = time.Now()
	cleanupStaleSessionsMu.Unlock()

	err := sessionruntime.ScanStaleSessions(ctx, maxAge)
	if err == nil && ctx.Err() != nil {
		err = contextutil.ErrorWithCause(ctx)
	}
	if err != nil {
		cleanupStaleSessionsMu.Lock()
		if cleanupAttempt == attempt {
			lastCleanupTime = time.Time{}
		}
		cleanupStaleSessionsMu.Unlock()
	}
	return err
}

// CleanupSessionLogs removes only expired, uniquely-created session log
// directories from root. It never scans or removes arbitrary files in the
// configured parent directory.
func CleanupSessionLogs(root string, maxAge time.Duration) {
	sessionruntime.CleanupSessionLogs(root, maxAge)
}
