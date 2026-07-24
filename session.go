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

	"golang.org/x/sync/errgroup"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
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
	swayPid    int
	dbusPid    int
	wlPastePid int
	swayCmd    *exec.Cmd
	dbusCmd    *exec.Cmd
	wlPasteCmd *exec.Cmd
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	stopped    bool
	unregister func()
}

// Session is the central orchestrator of perfuncted. It owns all backends and
// manages the desktop session lifecycle.
type Session struct {
	Screen    *ScreenBundle
	Input     *InputBundle
	Windows   *WindowBundle
	Outputs   *OutputBundle
	Clipboard *ClipboardBundle

	config       SessionConfig
	target       DesktopTarget
	env          env.Runtime
	tracer       *actionTracer
	infra        *sessionInfra
	capabilities map[Capability]CapabilityStatus

	ctx    context.Context
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
		return nil, errors.New("perfuncted: open: nil context")
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
		closeErr := s.Close()
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
		session:    s,
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
		status.Failure = err
		status.Available = err == nil
		if err == nil {
			status.Backend = fmt.Sprintf("%T", backend)
			status.Operations = supportedOperations(capability, backend)
		}
		s.capabilities[capability] = status
		s.setCapabilityFailure(capability, err)
		if err != nil && status.Required {
			return &CapabilityError{
				Capability: capability,
				Operation:  "open",
				Err:        err,
			}
		}
	}

	return nil
}

func supportedOperations(capability Capability, backend any) []string {
	if capability == CapabilityWindows {
		if reporter, ok := backend.(interface {
			SupportedOperations() []string
		}); ok {
			return reporter.SupportedOperations()
		}
	}
	return capabilityOperations(capability)
}

func (s *Session) bundleBase(capability Capability) bundleBase {
	return bundleBase{
		capability: capability,
		tracer:     s.tracer,
	}
}

func (s *Session) openCapability(capability Capability) (any, error) {
	switch capability {
	case CapabilityScreen:
		backend, err := openScreen(s.env)
		if err == nil {
			s.Screen.backend = backend
		}
		closeFailedBackend(backend, err)
		return backend, err
	case CapabilityInput:
		backend, err := openInput(s.env, 0, 0)
		if err == nil {
			s.Input.backend = backend
		}
		closeFailedBackend(backend, err)
		return backend, err
	case CapabilityWindows:
		backend, err := openWindow(s.env)
		if err == nil {
			s.Windows.backend = backend
		}
		closeFailedBackend(backend, err)
		return backend, err
	case CapabilityOutputs:
		backend, err := openOutput(s.env)
		if err == nil {
			s.Outputs.backend = backend
		}
		closeFailedBackend(backend, err)
		return backend, err
	case CapabilityClipboard:
		backend, err := openClipboard(s.env)
		if err == nil {
			s.Clipboard.backend = backend
		}
		closeFailedBackend(backend, err)
		return backend, err
	default:
		return nil, fmt.Errorf("unknown capability %q", capability)
	}
}

func closeFailedBackend(backend any, openErr error) {
	if openErr == nil || backend == nil {
		return
	}
	if closer, ok := backend.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

func (s *Session) setCapabilityFailure(capability Capability, err error) {
	switch capability {
	case CapabilityScreen:
		s.Screen.failure = err
	case CapabilityInput:
		s.Input.failure = err
	case CapabilityWindows:
		s.Windows.failure = err
	case CapabilityOutputs:
		s.Outputs.failure = err
	case CapabilityClipboard:
		s.Clipboard.failure = err
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
	if s == nil {
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
	if s.Has(CapabilityClipboard) {
		return s.Clipboard.pasteWithInputContext(ctx, text, s.Input)
	}
	return s.Input.typeContext(ctx, text)
}

// XDG returns the resolved XDG runtime directory for the session.
func (s *Session) XDG() string {
	if s.infra != nil {
		return s.infra.xdgDir
	}
	return os.Getenv("XDG_RUNTIME_DIR")
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

// Runtime returns the full environment snapshot used by this session.
func (s *Session) Runtime() env.Runtime {
	return s.env
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
	if err := os.Chmod(xdgDir, 0700); err != nil {
		os.RemoveAll(xdgDir)
		return nil, fmt.Errorf("session: chmod: %w", err)
	}

	logDir := config.LogDir
	if logDir == "" {
		logDir = filepath.Join(os.TempDir(), "perfuncted-logs")
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		os.RemoveAll(xdgDir)
		return nil, fmt.Errorf("session: mkdir logs: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	infra := &sessionInfra{
		xdgDir:    xdgDir,
		wlDisplay: "wayland-1",
		dbusAddr:  fmt.Sprintf("unix:path=%s/bus", xdgDir),
		logDir:    logDir,
		ctx:       ctx,
		cancel:    cancel,
	}

	pidPath := filepath.Join(xdgDir, sessionOwnerPIDFile)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		infra.stop()
		return nil, fmt.Errorf("session: write owner pidfile: %w", err)
	}

	infra.unregister = infra.CleanupOnSignal(infra.ctx)

	if err := infra.launchDBus(); err != nil {
		infra.stop()
		return nil, fmt.Errorf("session: dbus: %w", err)
	}

	swayConf := config.SwayConfigPath
	if swayConf == "" {
		swayConf, err = infra.resolveSwayConfig(mode, config.Resolution)
	}
	if err != nil {
		infra.stop()
		return nil, fmt.Errorf("session: sway config: %w", err)
	}

	if err := infra.launchSway(swayConf, mode); err != nil {
		infra.stop()
		return nil, fmt.Errorf("session: sway: %w", err)
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

func (i *sessionInfra) launchDBus() error {
	cmd := executil.CommandContext(i.ctx, "dbus-daemon", "--session",
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
	i.dbusPid = cmd.Process.Pid
	i.dbusCmd = cmd
	i.writeChildPID("dbus.pid", i.dbusPid)

	busPath := filepath.Join(i.xdgDir, "bus")
	if err := waitForFile(busPath, 100, 100*time.Millisecond); err != nil {
		return fmt.Errorf("dbus socket %s did not appear within 10s: %w", busPath, err)
	}
	return nil
}

func (i *sessionInfra) launchSway(confPath string, mode sessionMode) error {
	logPath := filepath.Join(i.logDir, "sway-session.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create log: %w", err)
	}

	cmd := executil.CommandContext(i.ctx, "sway", "--unsupported-gpu", "-c", confPath)
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
	i.swayPid = cmd.Process.Pid
	i.swayCmd = cmd
	i.writeChildPID("sway.pid", i.swayPid)
	logFileClose(logFile)

	socketPath := filepath.Join(i.xdgDir, i.wlDisplay)
	ipcGlob := filepath.Join(i.xdgDir, "sway-ipc.*.sock")
	g := new(errgroup.Group)
	g.Go(func() error {
		if err := waitForFile(socketPath, 150, 200*time.Millisecond); err != nil {
			return fmt.Errorf("wayland socket %s did not appear within 30s: %w", socketPath, err)
		}
		return nil
	})
	g.Go(func() error {
		if err := waitForGlob(ipcGlob, 150, 200*time.Millisecond); err != nil {
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
		i.wlPastePid = cmd.Process.Pid
		i.wlPasteCmd = cmd
		i.writeChildPID("wl-paste.pid", i.wlPastePid)
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
		resStr := fmt.Sprintf("%dx%d", res.X, res.Y)
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

	i.stopManagedProcess(i.wlPasteCmd, i.wlPastePid, 200*time.Millisecond)
	i.stopManagedProcess(i.swayCmd, i.swayPid, 500*time.Millisecond)
	i.stopManagedProcess(i.dbusCmd, i.dbusPid, 200*time.Millisecond)
	if i.xdgDir != "" {
		unmountSubdirs(i.xdgDir)
		if err := os.RemoveAll(i.xdgDir); err != nil {
			slog.Debug("session: remove xdg dir", "path", i.xdgDir, "error", err)
		}
	}
}

func (i *sessionInfra) stopManagedProcess(cmd *exec.Cmd, pid int, waitTimeout time.Duration) {
	(&managedProc{cmd: cmd, pid: pid}).stop(waitTimeout)
}

// ---------- managed process ----------

type managedProc struct {
	cmd *exec.Cmd
	pid int
}

func (m *managedProc) stop(waitTimeout time.Duration) {
	if m == nil || m.pid <= 0 {
		return
	}
	if err := syscall.Kill(-m.pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		slog.Debug("session: terminate process group", "pid", m.pid, "error", err)
	}
	if m.cmd == nil {
		time.Sleep(waitTimeout)
		return
	}
	if waitForProc(m.pid, waitTimeout) {
		return
	}
	if err := syscall.Kill(-m.pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		slog.Debug("session: kill process group", "pid", m.pid, "error", err)
	}
	waitForProc(m.pid, waitTimeout)
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

// CleanupStaleSessions removes perfuncted session directories older than
// maxAge when their recorded parent PID is no longer running.
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
			if now.Sub(fi.ModTime()) > staleNoPIDThreshold(maxAge) {
				reapSessionDir(d)
			}
			continue
		}
		pidStr := strings.TrimSpace(string(data))
		pid, perr := strconv.Atoi(pidStr)
		if perr != nil {
			fi, statErr := os.Stat(d)
			if statErr == nil && now.Sub(fi.ModTime()) > maxAge {
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
		(&managedProc{pid: pid}).stop(100 * time.Millisecond)
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

func staleNoPIDThreshold(maxAge time.Duration) time.Duration {
	if maxAge <= 0 || maxAge > noPIDFileReapGrace {
		return noPIDFileReapGrace
	}
	return maxAge
}

func waitForFile(path string, attempts int, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for i := 0; i < attempts; i++ {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if i == attempts-1 {
			break
		}
		<-ticker.C
	}
	return fmt.Errorf("%s did not appear within %s", path, time.Duration(attempts)*interval)
}

func waitForGlob(pattern string, attempts int, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for i := 0; i < attempts; i++ {
		if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
			return nil
		}
		if i == attempts-1 {
			break
		}
		<-ticker.C
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
