package perfuncted

import (
	"context"
	"time"

	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/clipboard"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/output"
	"github.com/nskaggs/perfuncted/v2/screen"
	"github.com/nskaggs/perfuncted/v2/window"
)

// Session is the lifecycle authority for a privately owned desktop session.
// Copying the wrapper aliases the same core; Close is shared by all copies.
type Session struct{ core *sessionCore }

func (s *Session) coreState() *sessionCore {
	if s == nil {
		return nil
	}
	if s.core == nil {
		return &sessionCore{}
	}
	return s.core
}
func wrapSession(s *sessionCore) *Session {
	if s == nil {
		return nil
	}
	return &Session{core: s}
}

// Open resolves the requested backends and optionally starts an isolated desktop.
func Open(ctx context.Context, opts ...Option) (*Session, error) {
	s, err := openSession(ctx, opts...)
	return wrapSession(s), err
}

// OpenDefault opens a session with the default convenience capabilities.
func OpenDefault(opts ...Option) (*Session, error) {
	s, err := openDefault(opts...)
	return wrapSession(s), err
}

// NewSessionForTesting transfers lifecycle responsibility for the supplied
// backend fakes to the new Session. Production code should use Open.
func NewSessionForTesting(screenshotter screen.Screenshotter, inputter input.Inputter, windowManager window.Manager, outputLister output.Lister, clipboardBackend clipboard.Clipboard, accessibilityBackend ...accessibility.Backend) *Session {
	return wrapSession(newSessionForTesting(screenshotter, inputter, windowManager, outputLister, clipboardBackend, accessibilityBackend...))
}

// ScreenBundle is a non-owning session-bound screen handle.
type ScreenBundle struct {
	screenHandle
	state *screenState
}

// InputBundle is a non-owning session-bound injection handle.
type InputBundle struct{ inputHandle }

// InputObservationBundle is a separately authorized observation handle.
type InputObservationBundle struct{ observationHandle }

// WindowBundle is a non-owning session-bound discovery/control handle.
type WindowBundle struct {
	windowHandle
	state *windowBundleState
}

// OutputBundle is a non-owning session-bound output discovery handle.
type OutputBundle struct{ state *outputState }

// ClipboardBundle is a non-owning session-bound clipboard handle.
type ClipboardBundle struct{ state *clipboardState }

// AccessibilityBundle is a non-owning session-bound semantic automation handle.
type AccessibilityBundle struct {
	accessibilityHandle
	state *accessibilityState
}

// Screen returns the screen capability handle.
func (s *Session) Screen() ScreenBundle {
	if s == nil || s.core == nil {
		return ScreenBundle{}
	}
	if s.core.screen == nil {
		return ScreenBundle{}
	}
	return ScreenBundle{s.core.screen.screenHandle, s.core.screen}
}

// Input returns the keyboard and pointer injection handle.
func (s *Session) Input() InputBundle {
	if s == nil || s.core == nil {
		return InputBundle{}
	}
	return InputBundle{s.core.input.Handle()}
}

// InputObservation returns the independent input observation handle.
func (s *Session) InputObservation() InputObservationBundle {
	if s == nil || s.core == nil {
		return InputObservationBundle{}
	}
	return InputObservationBundle{s.core.inputObservation.Handle()}
}

// Windows returns the window discovery and control handle.
func (s *Session) Windows() WindowBundle {
	if s == nil || s.core == nil {
		return WindowBundle{}
	}
	if s.core.windows == nil {
		return WindowBundle{}
	}
	return WindowBundle{s.core.windows.windowHandle, s.core.windows}
}

// Outputs returns the output discovery handle.
func (s *Session) Outputs() OutputBundle {
	if s == nil || s.core == nil {
		return OutputBundle{}
	}
	return OutputBundle{s.core.outputs}
}

// Clipboard returns the clipboard handle.
func (s *Session) Clipboard() ClipboardBundle {
	if s == nil || s.core == nil {
		return ClipboardBundle{}
	}
	return ClipboardBundle{s.core.clipboard}
}

// Accessibility returns the semantic accessibility handle.
func (s *Session) Accessibility() AccessibilityBundle {
	if s == nil || s.core == nil {
		return AccessibilityBundle{}
	}
	if s.core.accessibility == nil {
		return AccessibilityBundle{}
	}
	return AccessibilityBundle{s.core.accessibility.accessibilityHandle, s.core.accessibility}
}

// Window is a detached, non-owning handle to a stable session-bound target.
type Window struct{ state *windowState }

func (w *Window) privateState() *windowState {
	if w == nil {
		return nil
	}
	return w.state
}
func wrapWindow(w *windowState) *Window {
	if w == nil {
		return nil
	}
	return &Window{state: w}
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

// WindowClosed returns a condition bound to the supplied target's current
// private identity, rather than to a caller-overwritable wrapper.
func WindowClosed(target *Window) Condition { return windowClosed(target.privateState()) }

// CaptureWindow captures the current screen rectangle reported by a stable
// Session-owned window handle. It captures the visible desktop composite in
// that rectangle, including any windows drawn above the target; it does not
// retrieve hidden or minimized window contents.
func (s ScreenBundle) CaptureWindow(ctx context.Context, target *Window) (WindowCapture, error) {
	return s.state.CaptureWindow(ctx, target.privateState())
}

// CaptureWindowBackground reads a target-only buffer when the selected screen
// backend exposes that capability. It never activates the target and never
// falls back to a visible desktop crop.
func (s ScreenBundle) CaptureWindowBackground(ctx context.Context, target *Window) (WindowCapture, error) {
	return s.state.CaptureWindowBackground(ctx, target.privateState())
}

// CaptureWindowBackgroundTo captures one target-only background buffer and
// atomically publishes it as PNG at path.
func (s ScreenBundle) CaptureWindowBackgroundTo(ctx context.Context, target *Window, path string) (WindowCapture, error) {
	return s.state.CaptureWindowBackgroundTo(ctx, target.privateState(), path)
}

// CaptureWindowTo captures one stable window's visible screen rectangle and
// atomically publishes it as PNG at path. The captured content is the desktop
// composite, not a background-only buffer.
func (s ScreenBundle) CaptureWindowTo(ctx context.Context, target *Window, path string) (WindowCapture, error) {
	return s.state.CaptureWindowTo(ctx, target.privateState(), path)
}

// WithContext returns a non-owning view. A nil context is rejected on use.
func (s *Session) WithContext(ctx context.Context) *SessionView {
	return s.coreState().WithContext(ctx)
}

// WithTimeout returns a non-owning view with a batch timeout.
func (s *Session) WithTimeout(d time.Duration) *SessionView {
	return s.coreState().WithTimeout(d)
}

// Do executes steps in order against the currently focused application. It
// preflights known requirements but cannot guarantee application acceptance or
// atomic focus, and does not retry failed input.
func (s *Session) Do(steps ...Step) error {
	return s.coreState().Do(steps...)
}

// Type immediately sends literal text to the currently focused application.
func (s *Session) Type(text string) error {
	return s.coreState().Type(text)
}

// Key immediately sends one named key to the currently focused application.
func (s *Session) Key(name string) error {
	return s.coreState().Key(name)
}

// Window selects a window by exact, case-insensitive title for a targeted batch.
// Pass Require(CapabilityWindows) to OpenDefault to enable this operation.
func (s *Session) Window(title string) *WindowTarget {
	return s.coreState().Window(title)
}

// CaptureFailureBundle collects best-effort diagnostic artifacts into a
// directory and returns its path. Each artifact is attempted independently;
// the returned error reports collection failures after the successful files
// have been written. Its manifest records the effective timeout policy and
// whether each field came from SessionConfig or the library defaults.
func (s *Session) CaptureFailureBundle(ctx context.Context, options FailureBundleOptions) (string, error) {
	return s.coreState().CaptureFailureBundle(ctx, options)
}

// Close releases all backends and tears down any managed session infrastructure.
func (s *Session) Close() error {
	return s.coreState().Close()
}

// Has reports whether the session provides the given capability.
func (s *Session) Has(cap Capability) bool {
	return s.coreState().Has(cap)
}

// Capability returns the immutable resolution status for cap.
func (s *Session) Capability(cap Capability) CapabilityStatus {
	return s.coreState().Capability(cap)
}

// Capabilities returns every capability's immutable resolution status.
func (s *Session) Capabilities() []CapabilityStatus {
	return s.coreState().Capabilities()
}

// Timeouts returns the effective timing policy used by this session. The
// returned value is a copy and can be safely modified by the caller.
func (s *Session) Timeouts() TimeoutPolicy {
	return s.coreState().Timeouts()
}

// Target returns the exact immutable desktop target.
func (s *Session) Target() DesktopTarget {
	return s.coreState().Target()
}

// Env returns a copy of the process environment that routes child processes to
// the Session's target.
func (s *Session) Env() []string {
	return s.coreState().Env()
}

// Paste writes text through the clipboard when available and otherwise types
// it directly.
func (s *Session) Paste(ctx context.Context, text string) (result error) {
	return s.coreState().Paste(ctx, text)
}

// XDG returns the resolved XDG runtime directory for the session.
func (s *Session) XDG() string {
	return s.coreState().XDG()
}

// DBusAddress returns the session D-Bus bus address.
func (s *Session) DBusAddress() string {
	return s.coreState().DBusAddress()
}

// WaylandDisplay returns the session Wayland display name.
func (s *Session) WaylandDisplay() string {
	return s.coreState().WaylandDisplay()
}

// X11Display returns the session X11 display string.
func (s *Session) X11Display() string {
	return s.coreState().X11Display()
}

// InvokeAccessibilityActionAndWait invokes one explicitly resolved AT-SPI
// action and waits for an independent authoritative postcondition. The
// returned evidence describes only bounded wakeups; callers decide what the
// postcondition means.
func (s *Session) InvokeAccessibilityActionAndWait(
	ctx context.Context,
	root accessibility.NodeID,
	query accessibility.Query,
	actionName string,
	snapshotOptions accessibility.SnapshotOptions,
	postcondition Condition,
	waitOptions ...WaitOption,
) (AccessibilityActionReceipt, WaitEvidence, error) {
	return s.coreState().InvokeAccessibilityActionAndWait(ctx, root, query, actionName, snapshotOptions, postcondition, waitOptions...)
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

// Wait blocks until condition succeeds. Timeout and cancellation follow ctx;
// persistent observation failures are bounded, and terminal failures abort.
func (s *Session) Wait(
	ctx context.Context,
	condition Condition,
	options ...WaitOption,
) error {
	return s.coreState().Wait(ctx, condition, options...)
}

// WaitWithEvidence blocks until condition succeeds and returns bounded
// diagnostic evidence about the event-backed and polling wakeups observed.
// Events only wake the wait; the condition remains the authority.
func (s *Session) WaitWithEvidence(
	ctx context.Context,
	condition Condition,
	options ...WaitOption,
) (WaitEvidence, error) {
	return s.coreState().WaitWithEvidence(ctx, condition, options...)
}

// ID returns the opaque session-scoped identifier.
func (w *Window) ID() WindowID {
	return w.privateState().ID()
}

// Title returns the latest title observed through Info or discovery.
func (w *Window) Title() string {
	return w.privateState().Title()
}

// CachedInfo returns the last authoritative window metadata used to create or
// refresh this handle. It does not query the window manager. Callers that need
// current geometry or focus state should use Info; callers crossing into
// another semantic subsystem during a compositor stall can use this bounded
// snapshot without issuing a second discovery request.
func (w *Window) CachedInfo() window.Info {
	return w.privateState().CachedInfo()
}

// Info refreshes and returns authoritative window state.
func (w *Window) Info(ctx context.Context) (window.Info, error) {
	return w.privateState().Info(ctx)
}

// Activate focuses the window.
func (w *Window) Activate(ctx context.Context) error {
	return w.privateState().Activate(ctx)
}

// Move positions the window at x and y. Negative coordinates are valid for a
// monitor left of or above the primary one; a coordinate no backend can carry
// is rejected with ErrInvalidArgument.
func (w *Window) Move(ctx context.Context, x int, y int) error {
	return w.privateState().Move(ctx, x, y)
}

// Resize changes the window size to width by height. Both edges must be
// positive and within the geometry range every backend can represent; anything
// else is rejected with ErrInvalidArgument rather than sent to a compositor that
// would truncate it.
func (w *Window) Resize(
	ctx context.Context,
	width int,
	height int,
) error {
	return w.privateState().Resize(ctx, width, height)
}

// Close requests that the window close.
func (w *Window) Close(ctx context.Context) error {
	return w.privateState().Close(ctx)
}

// Minimize minimizes the window.
func (w *Window) Minimize(ctx context.Context) error {
	return w.privateState().Minimize(ctx)
}

// Maximize maximizes the window.
func (w *Window) Maximize(ctx context.Context) error {
	return w.privateState().Maximize(ctx)
}

// Fullscreen makes the window occupy the screen.
func (w *Window) Fullscreen(ctx context.Context) error {
	return w.privateState().Fullscreen(ctx)
}

// Unfullscreen restores the window from fullscreen mode.
func (w *Window) Unfullscreen(ctx context.Context) error {
	return w.privateState().Unfullscreen(ctx)
}

// Restore returns the window to its normal state.
func (w *Window) Restore(ctx context.Context) error {
	return w.privateState().Restore(ctx)
}

// WaitClosed waits until the authoritative backend no longer reports w.
func (w *Window) WaitClosed(ctx context.Context) error {
	return w.privateState().WaitClosed(ctx)
}

// List returns every window satisfying match.
func (s WindowBundle) List(
	ctx context.Context,
	match WindowMatch,
) ([]*Window, error) {
	values, err := s.state.List(ctx, match)
	if values == nil {
		return nil, err
	}
	result := make([]*Window, len(values))
	for i, v := range values {
		result[i] = wrapWindow(v)
	}
	return result, err
}

// Find returns exactly one matching window.
func (s WindowBundle) Find(
	ctx context.Context,
	match WindowMatch,
) (*Window, error) {
	v, err := s.state.Find(ctx, match)
	return wrapWindow(v), err
}

// Wait waits for match to identify exactly one window.
func (s WindowBundle) Wait(
	ctx context.Context,
	match WindowMatch,
	options ...WaitOption,
) (*Window, error) {
	v, err := s.state.Wait(ctx, match, options...)
	return wrapWindow(v), err
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

// List returns the displays visible to the active output backend.
func (b OutputBundle) List(ctx context.Context) ([]output.Info, error) { return b.state.List(ctx) }

// Get returns the current clipboard text.
func (b ClipboardBundle) Get(ctx context.Context) (string, error) { return b.state.Get(ctx) }

// Set replaces the clipboard text.
func (b ClipboardBundle) Set(ctx context.Context, text string) error { return b.state.Set(ctx, text) }

// WindowRoot resolves a managed window's exact accessibility subtree.
func (b AccessibilityBundle) WindowRoot(ctx context.Context, id string) (accessibility.WindowScope, error) {
	return b.state.WindowRoot(ctx, id)
}

// WindowRootFresh re-correlates a managed window from fresh state.
func (b AccessibilityBundle) WindowRootFresh(ctx context.Context, id string) (accessibility.WindowScope, error) {
	return b.state.WindowRootFresh(ctx, id)
}

// FindApplication resolves a unique application in the owning session.
func (b AccessibilityBundle) FindApplication(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	return b.state.FindApplication(ctx, filter)
}

// FindApplicationFresh resolves an application from uncached identity state.
func (b AccessibilityBundle) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	return b.state.FindApplicationFresh(ctx, filter)
}

// LocatorForWindow scopes a semantic locator to a managed window.
func (b AccessibilityBundle) LocatorForWindow(id string, selector accessibility.Selector, opts accessibility.SnapshotOptions) *AccessibilityLocator {
	return b.state.LocatorForWindow(id, selector, opts)
}

// LocatorForApplication scopes a semantic locator to a unique application.
func (b AccessibilityBundle) LocatorForApplication(filter accessibility.ApplicationFilter, selector accessibility.Selector, opts accessibility.SnapshotOptions) *AccessibilityLocator {
	return b.state.LocatorForApplication(filter, selector, opts)
}
