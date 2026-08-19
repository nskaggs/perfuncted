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
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	capabilityops "github.com/nskaggs/perfuncted/internal/capability"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
	"github.com/nskaggs/perfuncted/window"
)

//go:embed configs/headless.conf configs/nested.conf
var embeddedConfigs embed.FS

var (
	cleanupStaleSessionsMu sync.Mutex
	lastCleanupTime        time.Time
)

const (
	sessionOwnerPIDFile     = "perfuncted.pid"
	noPIDFileReapGrace      = 5 * time.Minute
	cleanupStaleMinInterval = 30 * time.Second
)

var sessionChildPIDFiles = []string{
	"dbus.pid",
	"sway.pid",
	"wl-paste.pid",
}

type sessionMode int

const (
	sessionModeHeadless sessionMode = iota
	sessionModeNested
)

// sessionInfra holds the infrastructure state for a managed session.
type sessionInfra struct {
	xdgDir     string
	wlDisplay  string
	dbusAddr   string
	logDir     string
	swayCmd    *exec.Cmd
	dbusCmd    *exec.Cmd
	wlPasteCmd *exec.Cmd
	ctx        context.Context //nolint:containedctx // infrastructure owns this context
	cancel     context.CancelFunc
	mu         sync.Mutex
	stopped    bool
	unregister func()
}

// Session is the central orchestrator of perfuncted. It owns all backends and
// manages the desktop session lifecycle.
type Session struct {
	// Screen exposes screen-capture operations for this session.
	Screen *ScreenBundle
	// Input exposes keyboard and pointer operations for this session.
	Input *InputBundle
	// Windows exposes window discovery and control for this session.
	Windows *WindowBundle
	// Outputs exposes display-output discovery for this session.
	Outputs *OutputBundle
	// Clipboard exposes clipboard access for this session.
	Clipboard *ClipboardBundle

	config       SessionConfig
	target       DesktopTarget
	env          env.Runtime
	tracer       *actionTracer
	infra        *sessionInfra
	capabilities map[Capability]CapabilityStatus

	ctx    context.Context //nolint:containedctx // session owns this context
	cancel context.CancelFunc

	lifecycleMu sync.Mutex
	closed      bool
	apps        []*Application

	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error

	hubOnce sync.Once
	hubMu   sync.RWMutex
	hub     *invalidationHub
}

// Open creates a new Session, resolving backends and optionally starting
// an isolated desktop session.
func Open(ctx context.Context, opts ...Option) (*Session, error) {
	if ctx == nil {
		return nil, fmt.Errorf("perfuncted: open: %w: nil context", ErrInvalidArgument)
	}

	cfg := openConfig{
		required: make(map[Capability]struct{}),
		optional: make(map[Capability]struct{}),
		target: targetSelection{
			kind: TargetHost,
		},
	}
	for _, option := range opts {
		if option == nil {
			continue
		}
		if err := option(&cfg); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s := &Session{
		config:       cfg.target.config,
		capabilities: make(map[Capability]CapabilityStatus, len(allCapabilities)),
		closeDone:    make(chan struct{}),
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())

	if cfg.trace {
		s.tracer = newActionTracer(cfg.traceOut, cfg.logger, cfg.traceDelay)
	}

	if err := s.resolveRuntime(ctx, cfg.target); err != nil {
		s.cancel()
		return nil, err
	}

	if err := s.initializeCapabilities(cfg); err != nil {
		closeErr := s.Close() //nolint:contextcheck // Close owns its shutdown contexts
		return nil, errors.Join(err, closeErr)
	}
	if err := ctx.Err(); err != nil {
		closeErr := s.Close() //nolint:contextcheck // Close owns its shutdown contexts
		return nil, errors.Join(err, closeErr)
	}

	return s, nil
}

func (s *Session) resolveRuntime(ctx context.Context, target targetSelection) error {
	switch target.kind {
	case TargetHost:
		s.env = env.Current()
	case TargetExplicit:
		s.env = env.FromEnviron(target.target.Env())
	case TargetHeadless:
		CleanupStaleSessions(24 * time.Hour)
		infra, err := s.startSession(ctx, sessionModeHeadless, target.config)
		if err != nil {
			return err
		}
		s.infra = infra
		s.env = env.Current().WithSession(infra.xdgDir, infra.wlDisplay, infra.dbusAddr)
	case TargetNested:
		CleanupStaleSessions(24 * time.Hour)
		infra, err := s.startSession(ctx, sessionModeNested, target.config)
		if err != nil {
			return err
		}
		s.infra = infra
		s.env = env.Current().WithSession(infra.xdgDir, infra.wlDisplay, infra.dbusAddr)
	default:
		return fmt.Errorf("perfuncted: unknown target kind %q", target.kind)
	}
	s.target = DesktopTarget{
		kind: target.kind,
		env:  s.env.EnvList(),
	}
	return nil
}

func (s *Session) initializeCapabilities(cfg openConfig) error {
	s.Screen = &ScreenBundle{bundleBase: s.bundleBase(CapabilityScreen)}
	s.Input = &InputBundle{bundleBase: s.bundleBase(CapabilityInput)}
	s.Windows = &WindowBundle{
		bundleBase: s.bundleBase(CapabilityWindows),
	}
	s.Outputs = &OutputBundle{bundleBase: s.bundleBase(CapabilityOutputs)}
	s.Clipboard = &ClipboardBundle{bundleBase: s.bundleBase(CapabilityClipboard)}

	for _, capability := range allCapabilities {
		_, required := cfg.required[capability]
		_, optional := cfg.optional[capability]
		s.capabilities[capability] = CapabilityStatus{
			Capability: capability,
			Requested:  required || optional,
			Required:   required,
		}
	}

	for _, capability := range allCapabilities {
		status := s.capabilities[capability]
		if !status.Requested {
			continue
		}
		backend, err := s.openCapability(capability)
		if err != nil {
			status.Failure = errors.Join(ErrUnavailable, err)
		}
		status.Available = err == nil
		if err == nil {
			status.Backend = fmt.Sprintf("%T", backend)
			status.Operations = slices.Clone(supportedOperations(capability, backend))
		}
		s.capabilities[capability] = status
		if err != nil && status.Required {
			return &CapabilityError{
				Capability: capability,
				Operation:  "open",
				Err:        status.Failure,
			}
		}
	}

	return nil
}

func supportedOperations(capability Capability, backend any) []string {
	if reporter, ok := backend.(interface {
		SupportedOperations() []string
	}); ok {
		operations := slices.Clone(reporter.SupportedOperations())
		if capability == CapabilityWindows {
			if _, ok := backend.(interface {
				ActiveTitle(context.Context) (string, error)
			}); ok {
				operations = appendUniqueOperation(operations, "active-title")
			}
			if _, ok := backend.(interface{ Sync(context.Context) error }); ok {
				operations = appendUniqueOperation(operations, "sync")
			}
			if _, ok := backend.(interface {
				InfoByID(context.Context, string) (window.Info, error)
			}); ok {
				operations = appendUniqueOperation(operations, "info")
			}
		}
		return operations
	}
	if capability == CapabilityWindows {
		operations := capabilityops.Operations(
			"windows",
			"sync",
			"info",
			"activate",
			"move",
			"resize",
			"close",
			"minimize",
			"maximize",
			"fullscreen",
			"restore",
		)
		if _, ok := backend.(window.IDManager); ok {
			operations = capabilityops.Operations("windows", "sync")
		}
		if _, ok := backend.(interface{ Sync(context.Context) error }); ok {
			operations = append(operations, "sync")
		}
		return operations
	}
	operations := capabilityOperations(capability)
	return operations
}

func appendUniqueOperation(operations []string, operation string) []string {
	if slices.Contains(operations, operation) {
		return operations
	}
	return append(operations, operation)
}

func (s *Session) bundleBase(capability Capability) bundleBase {
	return bundleBase{
		session:    s,
		capability: capability,
	}
}

func (s *Session) isClosed() bool {
	if s == nil {
		return true
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.closed || s.ctx == nil
}

func (s *Session) ensureOpen() error {
	if s.isClosed() {
		return ErrSessionClosed
	}
	return nil
}

func (s *Session) openCapability(capability Capability) (any, error) {
	switch capability {
	case CapabilityScreen:
		backend, err := openScreen(s.env)
		backend, err = validateBackend(capability, backend, err)
		if err == nil {
			s.Screen.backend = backend
		}
		closeFailedBackend(backend, err)
		return backend, err
	case CapabilityInput:
		backend, err := openInput(s.env, 0, 0)
		backend, err = validateBackend(capability, backend, err)
		if err == nil {
			s.Input.backend = backend
		}
		closeFailedBackend(backend, err)
		return backend, err
	case CapabilityWindows:
		backend, err := openWindow(s.env)
		backend, err = validateBackend(capability, backend, err)
		if err == nil {
			s.Windows.backend = backend
		}
		closeFailedBackend(backend, err)
		return backend, err
	case CapabilityOutputs:
		backend, err := openOutput(s.env)
		backend, err = validateBackend(capability, backend, err)
		if err == nil {
			s.Outputs.backend = backend
		}
		closeFailedBackend(backend, err)
		return backend, err
	case CapabilityClipboard:
		backend, err := openClipboard(s.env)
		backend, err = validateBackend(capability, backend, err)
		if err == nil {
			s.Clipboard.backend = backend
		}
		closeFailedBackend(backend, err)
		return backend, err
	default:
		return nil, fmt.Errorf("unknown capability %q", capability)
	}
}

func validateBackend[T any](capability Capability, backend T, err error) (T, error) {
	if err == nil && nilBackend(backend) {
		err = nilBackendError(capability)
	}
	return backend, err
}

func nilBackend(backend any) bool {
	if backend == nil {
		return true
	}
	value := reflect.ValueOf(backend)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func nilBackendError(capability Capability) error {
	return fmt.Errorf("perfuncted: %s backend returned nil: %w", capability, ErrUnavailable)
}

func closeFailedBackend(backend any, openErr error) {
	if openErr == nil || nilBackend(backend) {
		return
	}
	if closer, ok := backend.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

// Close releases all backends and tears down any managed session infrastructure.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closeErr = s.close()
		if s.closeDone != nil {
			close(s.closeDone)
		}
	})
	if s.closeDone != nil {
		<-s.closeDone
	}
	return s.closeErr
}

func (s *Session) close() error {
	s.lifecycleMu.Lock()
	s.closed = true
	apps := make([]*Application, len(s.apps))
	copy(apps, s.apps)
	s.lifecycleMu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}

	var errs []error
	for i := len(apps) - 1; i >= 0; i-- {
		if err := s.stopApplication(apps[i]); err != nil {
			errs = append(errs, err)
		}
	}
	if s.Screen != nil && s.Screen.backend != nil {
		errs = append(errs, s.Screen.backend.Close())
	}
	if s.Input != nil && s.Input.backend != nil {
		errs = append(errs, s.Input.backend.Close())
	}
	if s.Windows != nil {
		errs = append(errs, s.Windows.close())
	}
	if s.Outputs != nil {
		errs = append(errs, s.Outputs.close())
	}
	if s.Clipboard != nil {
		errs = append(errs, s.Clipboard.close())
	}

	if s.infra != nil {
		s.infra.stop()
	}

	return errors.Join(errs...)
}

// Has reports whether the session provides the given capability.
func (s *Session) Has(cap Capability) bool {
	if s == nil || s.isClosed() {
		return false
	}
	status, ok := s.capabilities[cap]
	return ok && status.Available
}

// Capability returns the immutable resolution status for cap.
func (s *Session) Capability(cap Capability) CapabilityStatus {
	if s == nil {
		return CapabilityStatus{
			Capability: cap,
			Failure:    ErrNilSession,
		}
	}
	status, ok := s.capabilities[cap]
	if !ok {
		return CapabilityStatus{Capability: cap}
	}
	return status.clone()
}

// Capabilities returns every capability's immutable resolution status.
func (s *Session) Capabilities() []CapabilityStatus {
	statuses := make([]CapabilityStatus, 0, len(allCapabilities))
	for _, capability := range allCapabilities {
		statuses = append(statuses, s.Capability(capability))
	}
	return statuses
}

// Target returns the exact immutable desktop target.
func (s *Session) Target() DesktopTarget {
	if s == nil {
		return DesktopTarget{}
	}
	return s.target.clone()
}

// Env returns a copy of the process environment that routes child processes to
// the Session's target.
func (s *Session) Env() []string {
	if s == nil {
		return []string{}
	}
	return s.env.EnvList()
}

// Paste writes text through the clipboard when available and otherwise types
// it directly.
func (s *Session) Paste(ctx context.Context, text string) error {
	if s == nil {
		return ErrNilSession
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: paste: %w: nil context", ErrInvalidArgument)
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if s.Has(CapabilityClipboard) {
		return s.Clipboard.pasteWithInputContext(ctx, text, s.Input)
	}
	if s.Input == nil {
		return ErrUnavailable
	}
	return s.Input.TypeLiteral(ctx, text)
}

// XDG returns the resolved XDG runtime directory for the session.
func (s *Session) XDG() string {
	if s == nil {
		return ""
	}
	return s.env.Get("XDG_RUNTIME_DIR")
}

// DBusAddress returns the session D-Bus bus address.
func (s *Session) DBusAddress() string {
	return s.env.Get("DBUS_SESSION_BUS_ADDRESS")
}

// WaylandDisplay returns the session Wayland display name.
func (s *Session) WaylandDisplay() string {
	return s.env.Get("WAYLAND_DISPLAY")
}

// X11Display returns the session X11 display string.
func (s *Session) X11Display() string {
	return s.env.Display()
}

// ---------- infrastructure launchers ----------

func (s *Session) startSession(
	ctx context.Context,
	mode sessionMode,
	config SessionConfig,
) (*sessionInfra, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("session: startup: %w", err)
	}
	if config.Resolution == (image.Point{}) {
		config.Resolution = image.Pt(1024, 768)
	}

	xdgDir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		return nil, fmt.Errorf("session: mkdirtemp: %w", err)
	}
	if chmodErr := os.Chmod(xdgDir, 0700); chmodErr != nil {
		os.RemoveAll(xdgDir)
		return nil, fmt.Errorf("session: chmod: %w", chmodErr)
	}

	logDir := config.LogDir
	if logDir == "" {
		logDir = filepath.Join(os.TempDir(), "perfuncted-logs")
	}
	if mkdirErr := os.MkdirAll(logDir, 0755); mkdirErr != nil {
		os.RemoveAll(xdgDir)
		return nil, fmt.Errorf("session: mkdir logs: %w", mkdirErr)
	}

	infraCtx, cancel := context.WithCancel(context.Background())
	infra := &sessionInfra{
		xdgDir:    xdgDir,
		wlDisplay: "wayland-1",
		dbusAddr:  fmt.Sprintf("unix:path=%s/bus", xdgDir),
		logDir:    logDir,
		ctx:       infraCtx,
		cancel:    cancel,
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

	if launchErr := infra.launchDBus(ctx); launchErr != nil {
		infra.stop()
		return nil, fmt.Errorf("session: dbus: %w", launchErr)
	}

	swayConf := config.SwayConfigPath
	if swayConf == "" {
		swayConf, err = infra.resolveSwayConfig(mode, config.Resolution)
	}
	if err != nil {
		infra.stop()
		return nil, fmt.Errorf("session: sway config: %w", err)
	}

	if err := infra.launchSway(ctx, swayConf, mode); err != nil {
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
	i.dbusCmd = cmd
	i.writeChildPID("dbus.pid", cmd.Process.Pid)

	busPath := filepath.Join(i.xdgDir, "bus")
	if err := waitForFile(ctx, busPath, 100, 100*time.Millisecond); err != nil {
		return fmt.Errorf("dbus socket %s did not appear within 10s: %w", busPath, err)
	}
	return nil
}

func (i *sessionInfra) launchSway(
	ctx context.Context,
	confPath string,
	mode sessionMode,
) error {
	logPath := filepath.Join(i.logDir, "sway-session.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create log: %w", err)
	}

	cmd := executil.CommandContext(i.ctx, "sway", "--unsupported-gpu", "-c", confPath) //nolint:contextcheck // process lifetime outlives startup context
	runtime := env.Current().
		With("XDG_RUNTIME_DIR", i.xdgDir).
		With("DBUS_SESSION_BUS_ADDRESS", i.dbusAddr).
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
	i.swayCmd = cmd
	i.writeChildPID("sway.pid", cmd.Process.Pid)
	logFileClose(logFile)

	socketPath := filepath.Join(i.xdgDir, i.wlDisplay)
	ipcGlob := filepath.Join(i.xdgDir, "sway-ipc.*.sock")
	g := new(errgroup.Group)
	g.Go(func() error {
		if err := waitForFile(ctx, socketPath, 150, 200*time.Millisecond); err != nil {
			return fmt.Errorf("wayland socket %s did not appear within 30s: %w", socketPath, err)
		}
		return nil
	})
	g.Go(func() error {
		if err := waitForGlob(ctx, ipcGlob, 150, 200*time.Millisecond); err != nil {
			return fmt.Errorf("sway IPC socket in %s did not appear within 30s: %w", i.xdgDir, err)
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
		i.wlPasteCmd = cmd
		i.writeChildPID("wl-paste.pid", cmd.Process.Pid)
		return
	}
	slog.Warn("wl-paste helper failed to start", "error", err)
}

// ---------- session lifecycle helpers ----------

func (i *sessionInfra) writeChildPID(name string, pid int) {
	if i == nil || i.xdgDir == "" || pid <= 0 {
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
	i.mu.Lock()
	if i.stopped {
		i.mu.Unlock()
		return
	}
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

	i.stopManagedProcess(i.wlPasteCmd, 200*time.Millisecond)
	i.stopManagedProcess(i.swayCmd, 500*time.Millisecond)
	i.stopManagedProcess(i.dbusCmd, 200*time.Millisecond)
	if i.xdgDir != "" {
		unmountSubdirs(i.xdgDir)
		if err := os.RemoveAll(i.xdgDir); err != nil {
			slog.Debug("session: remove xdg dir", "path", i.xdgDir, "error", err)
		}
	}
}

func (i *sessionInfra) stopManagedProcess(cmd *exec.Cmd, waitTimeout time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	(&managedProc{cmd: cmd, pid: cmd.Process.Pid}).stop(waitTimeout)
}

// ---------- managed process ----------

type managedProc struct {
	cmd *exec.Cmd
	pid int

	mu        sync.Mutex
	groupGone bool
}

func (m *managedProc) stop(waitTimeout time.Duration) {
	if m == nil || m.pid <= 0 {
		return
	}
	if err := m.signal(syscall.SIGTERM); err != nil {
		slog.Debug("session: terminate process group", "pid", m.pid, "error", err)
	}
	leaderExited := false
	if m.cmd == nil {
		leaderExited = !pidAlive(m.pid)
	} else {
		leaderExited = waitForProc(m.pid, waitTimeout)
	}
	if leaderExited {
		if m.waitGroupTimeout(waitTimeout) {
			return
		}
	}
	if err := m.signal(syscall.SIGKILL); err != nil {
		slog.Debug("session: kill process group", "pid", m.pid, "error", err)
	}
	if m.cmd != nil {
		waitForProc(m.pid, waitTimeout)
	}
	_ = m.waitGroupTimeout(waitTimeout)
}

func processGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (m *managedProc) groupAlive() bool {
	if m == nil || m.pid <= 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.groupGone {
		return false
	}
	if !processGroupAlive(m.pid) {
		m.groupGone = true
		return false
	}
	return true
}

func (m *managedProc) signal(signal syscall.Signal) error {
	if m == nil || m.pid <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.groupGone {
		return nil
	}
	if !processGroupAlive(m.pid) {
		m.groupGone = true
		return nil
	}
	err := syscall.Kill(-m.pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		m.groupGone = true
		return nil
	}
	return err
}

func (m *managedProc) waitGroup(ctx context.Context) error {
	if m == nil || m.pid <= 0 {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("perfuncted: wait process group: %w: nil context", ErrInvalidArgument)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !m.groupAlive() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *managedProc) waitGroupTimeout(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !m.groupAlive() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForProc(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		var status syscall.WaitStatus
		waited, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		switch {
		case waited == pid:
			return true
		case errors.Is(err, syscall.ECHILD):
			return true
		case errors.Is(err, syscall.EINTR):
			continue
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ---------- stale session cleanup ----------

// CleanupStaleSessions removes perfuncted session directories immediately when
// their recorded parent PID is no longer running. Missing ownership metadata
// gets a five-minute creation grace; malformed metadata uses maxAge, clamped
// to that same minimum.
func CleanupStaleSessions(maxAge time.Duration) {
	cleanupStaleSessionsMu.Lock()
	defer cleanupStaleSessionsMu.Unlock()

	if time.Since(lastCleanupTime) < cleanupStaleMinInterval {
		return
	}
	lastCleanupTime = time.Now()

	matches, err := filepath.Glob(nestedSessionPattern())
	if err != nil {
		slog.Warn("unable to glob nested sessions", "error", err)
		return
	}
	now := time.Now()
	for _, d := range matches {
		pidPath := filepath.Join(d, sessionOwnerPIDFile)
		data, err := os.ReadFile(pidPath)
		if err != nil {
			fi, statErr := os.Stat(d)
			if statErr != nil {
				continue
			}
			if now.Sub(fi.ModTime()) > noPIDFileReapGrace {
				reapSessionDir(d)
			}
			continue
		}
		pidStr := strings.TrimSpace(string(data))
		pid, perr := strconv.Atoi(pidStr)
		if perr != nil {
			fi, statErr := os.Stat(d)
			if statErr == nil && now.Sub(fi.ModTime()) > staleMalformedPIDThreshold(maxAge) {
				reapSessionDir(d)
			}
			continue
		}
		if !pidAlive(pid) {
			reapSessionDir(d)
			continue
		}
	}
}

func reapSessionDir(dir string) {
	for _, name := range sessionChildPIDFiles {
		pid, err := readPIDFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if !stopRecordedProcess(pid, dir, 100*time.Millisecond) {
			slog.Debug(
				"session: skip stale child with mismatched runtime directory",
				"pid", pid,
				"path", dir,
			)
			continue
		}
	}
	unmountSubdirs(dir)
	if err := os.RemoveAll(dir); err != nil {
		slog.Debug("session: reap remove dir", "path", dir, "error", err)
	}
}

func unmountSubdirs(dir string) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return
	}
	prefix := dir + "/"
	var mounts []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mountpoint := fields[4]
		if strings.HasPrefix(mountpoint, prefix) || mountpoint == dir {
			mounts = append(mounts, mountpoint)
		}
	}
	for i := len(mounts) - 1; i >= 0; i-- {
		exec.Command("fusermount", "-u", mounts[i]).Run() //nolint:errcheck
	}
}

func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid in %s", path)
	}
	return pid, nil
}

func processUsesRuntimeDir(pid int, dir string) bool {
	return processUsesRuntimeDirAt("/proc", pid, dir)
}

type processSnapshot struct {
	pid       int
	pgid      int
	startTime uint64
}

type processGroupOwner struct {
	pgid    int
	dir     string
	leader  processSnapshot
	members map[processSnapshot]struct{}
}

func newProcessGroupOwner(pid int, dir string) (*processGroupOwner, bool) {
	if pid <= 0 || dir == "" || !processUsesRuntimeDir(pid, dir) {
		return nil, false
	}
	leader, err := readProcessSnapshotAt("/proc", pid)
	if err != nil || leader.pgid != pid {
		return nil, false
	}
	return &processGroupOwner{
		pgid:    pid,
		dir:     dir,
		leader:  leader,
		members: make(map[processSnapshot]struct{}),
	}, true
}

func (o *processGroupOwner) refresh() bool {
	if o == nil || o.pgid <= 0 || o.dir == "" {
		return false
	}
	processes := processGroupMembersAt("/proc", o.pgid)
	leaderPresent := false
	leaderOwned := false
	ownedMember := false
	for _, process := range processes {
		if process.pid == o.leader.pid {
			if process.startTime != o.leader.startTime {
				return false
			}
			leaderPresent = true
			leaderOwned = processUsesRuntimeDirAt("/proc", process.pid, o.dir)
			continue
		}
		if _, tracked := o.members[process]; tracked &&
			processUsesRuntimeDirAt("/proc", process.pid, o.dir) {
			ownedMember = true
		}
	}
	if leaderPresent && leaderOwned {
		ownedMember = true
		for _, process := range processes {
			if process.pid == o.leader.pid {
				continue
			}
			if processUsesRuntimeDirAt("/proc", process.pid, o.dir) {
				o.members[process] = struct{}{}
			}
		}
	}
	return ownedMember
}

func processGroupMembersAt(procRoot string, pgid int) []processSnapshot {
	if procRoot == "" || pgid <= 0 {
		return nil
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	members := make([]processSnapshot, 0)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		process, err := readProcessSnapshotAt(procRoot, pid)
		if err == nil && process.pgid == pgid {
			members = append(members, process)
		}
	}
	return members
}

func readProcessSnapshotAt(procRoot string, pid int) (processSnapshot, error) {
	if procRoot == "" || pid <= 0 {
		return processSnapshot{}, fmt.Errorf("invalid process snapshot target")
	}
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return processSnapshot{}, err
	}
	stat := string(data)
	commEnd := strings.LastIndexByte(stat, ')')
	if commEnd < 0 || commEnd+2 >= len(stat) {
		return processSnapshot{}, fmt.Errorf("invalid process stat for %d", pid)
	}
	fields := strings.Fields(stat[commEnd+2:])
	if len(fields) <= 19 {
		return processSnapshot{}, fmt.Errorf("invalid process stat for %d", pid)
	}
	pgid, err := strconv.Atoi(fields[2])
	if err != nil || pgid <= 0 {
		return processSnapshot{}, fmt.Errorf("invalid process group for %d", pid)
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return processSnapshot{}, fmt.Errorf("invalid process start time for %d", pid)
	}
	return processSnapshot{pid: pid, pgid: pgid, startTime: startTime}, nil
}

func processUsesRuntimeDirAt(procRoot string, pid int, dir string) bool {
	if pid <= 0 || dir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "environ"))
	if err != nil {
		return false
	}
	want := "XDG_RUNTIME_DIR=" + dir
	for value := range strings.SplitSeq(string(data), "\x00") {
		if value == want {
			return true
		}
	}
	return false
}

func stopRecordedProcess(pid int, dir string, grace time.Duration) bool {
	owner, ok := newProcessGroupOwner(pid, dir)
	if !ok || !owner.refresh() {
		return false
	}
	proc := &managedProc{pid: pid}
	if err := proc.signal(syscall.SIGTERM); err != nil {
		slog.Debug("session: terminate stale process group", "pid", pid, "error", err)
	}

	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !processGroupAlive(pid) {
			return true
		}
		if !owner.refresh() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !processGroupAlive(pid) || !owner.refresh() {
		return true
	}
	if err := proc.signal(syscall.SIGKILL); err != nil {
		slog.Debug("session: kill stale process group", "pid", pid, "error", err)
	}
	_ = proc.waitGroupTimeout(grace)
	return true
}

func staleMalformedPIDThreshold(maxAge time.Duration) time.Duration {
	if maxAge < noPIDFileReapGrace {
		return noPIDFileReapGrace
	}
	return maxAge
}

func waitForFile(
	ctx context.Context,
	path string,
	attempts int,
	interval time.Duration,
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return fmt.Errorf("%s did not appear within %s", path, time.Duration(attempts)*interval)
}

func waitForGlob(
	ctx context.Context,
	pattern string,
	attempts int,
	interval time.Duration,
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
			return nil
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return fmt.Errorf("pattern %s did not match within %s", pattern, time.Duration(attempts)*interval)
}

func logFileClose(f *os.File) {
	if f != nil {
		if err := f.Close(); err != nil {
			slog.Debug("session: close log file", "error", err)
		}
	}
}
