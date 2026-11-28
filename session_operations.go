package perfuncted

import (
	context "context"
	"errors"
	"fmt"
	accessibility "github.com/nskaggs/perfuncted/v2/accessibility"
	find "github.com/nskaggs/perfuncted/v2/find"
	input "github.com/nskaggs/perfuncted/v2/input"
	output "github.com/nskaggs/perfuncted/v2/output"
	screen "github.com/nskaggs/perfuncted/v2/screen"
	window "github.com/nskaggs/perfuncted/v2/window"
	image "image"
	color "image/color"
	"sync"
	time "time"
)

// WithContext returns a copy bound to ctx without changing the original.
func (h *Session) WithContext(ctx context.Context) *Session {
	if h == nil {
		h = &Session{}
	}
	copy := *h
	copy.scope = copy.scope.withContext(ctx)
	return &copy
}

// WithTimeout returns a copy with an operation timeout override.
func (h *Session) WithTimeout(d time.Duration) *Session {
	if h == nil {
		h = &Session{}
	}
	copy := *h
	copy.scope = copy.scope.withTimeout(d)
	return &copy
}

// CaptureFailureBundle collects best-effort diagnostic artifacts into a
// directory and returns its path. Each artifact is attempted independently;
// the returned error reports collection failures after the successful files
// have been written. Its manifest records the effective timeout policy and
// whether each field came from SessionConfig or the library defaults.
func (h *Session) CaptureFailureBundle(options FailureBundleOptions) (r0 string, err error) {
	ctx, finish, err := h.operationScope().begin(scopeDiagnostic, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.coreState().CaptureFailureBundle(ctx, options)
}

// InvokeAccessibilityActionAndWait invokes one explicitly resolved AT-SPI
// action and waits for an independent authoritative postcondition. The
// returned evidence describes only bounded wakeups; callers decide what the
// postcondition means.
func (h *Session) InvokeAccessibilityActionAndWait(root accessibility.NodeID, query accessibility.Query, actionName string, snapshotOptions accessibility.SnapshotOptions, postcondition Condition, waitOptions ...WaitOption) (r0 accessibility.ActionReceipt, r1 WaitEvidence, err error) {
	ctx, finish, err := h.operationScope().begin(scopeBatch, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.coreState().InvokeAccessibilityActionAndWait(ctx, root, query, actionName, snapshotOptions, postcondition, waitOptions...)
}

// Launch performs the scoped Launch operation.
func (h *Session) Launch(command Command) (r0 *Application, err error) {
	ctx, finish, err := h.operationScope().begin(scopeStartup, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.launchContext(ctx, command)
}

// Paste writes text through the clipboard when available and otherwise types
// it directly.
func (h *Session) Paste(text string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.coreState().Paste(ctx, text)
}

// Wait blocks until condition succeeds. Timeout and cancellation follow ctx;
// persistent observation failures are bounded, and terminal failures abort.
func (h *Session) Wait(condition Condition, options ...WaitOption) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.coreState().Wait(ctx, condition, options...)
}

// WaitWithEvidence blocks until condition succeeds and returns bounded
// diagnostic evidence about the event-backed and polling wakeups observed.
// Events only wake the wait; the condition remains the authority.
func (h *Session) WaitWithEvidence(condition Condition, options ...WaitOption) (r0 WaitEvidence, err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.coreState().WaitWithEvidence(ctx, condition, options...)
}

// WithContext returns a copy bound to ctx without changing the original.
func (h InputBundle) WithContext(ctx context.Context) InputBundle {
	h.scope = h.scope.withContext(ctx)
	return h
}

// WithTimeout returns a copy with an operation timeout override.
func (h InputBundle) WithTimeout(d time.Duration) InputBundle {
	h.scope = h.scope.withTimeout(d)
	return h
}

// ClickCenter performs the scoped ClickCenter operation.
func (h InputBundle) ClickCenter(rect image.Rectangle) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.ClickCenter(ctx, rect)
}

// DoubleClick performs the scoped DoubleClick operation.
func (h InputBundle) DoubleClick(x int, y int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.DoubleClick(ctx, x, y)
}

// DragAndDrop performs the scoped DragAndDrop operation.
func (h InputBundle) DragAndDrop(x1 int, y1 int, x2 int, y2 int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.DragAndDrop(ctx, x1, y1, x2, y2)
}

// DragAndDropSmooth performs the scoped DragAndDropSmooth operation.
func (h InputBundle) DragAndDropSmooth(start image.Point, end image.Point, o input.MotionOptions) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMotion, o.Duration)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.DragAndDropSmooth(ctx, start, end, o)
}

// KeyDown performs the scoped KeyDown operation.
func (h InputBundle) KeyDown(key string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.KeyDown(ctx, key)
}

// KeyUp performs the scoped KeyUp operation.
func (h InputBundle) KeyUp(key string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.KeyUp(ctx, key)
}

// Click performs the scoped MouseClick operation.
func (h InputBundle) Click(x int, y int, button ...int) (err error) {
	if len(button) > 1 {
		err = ErrInvalidArgument
		return
	}
	selectedButton := 1
	if len(button) == 1 {
		selectedButton = button[0]
	}
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.MouseClick(ctx, x, y, selectedButton)
}

// MouseDown performs the scoped MouseDown operation.
func (h InputBundle) MouseDown(button int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.MouseDown(ctx, button)
}

// MouseMove performs the scoped MouseMove operation.
func (h InputBundle) MouseMove(x int, y int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.MouseMove(ctx, x, y)
}

// MouseMoveSmooth performs the scoped MouseMoveSmooth operation.
func (h InputBundle) MouseMoveSmooth(start *image.Point, end image.Point, o input.MotionOptions) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMotion, o.Duration)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.MouseMoveSmooth(ctx, start, end, o)
}

// MouseUp performs the scoped MouseUp operation.
func (h InputBundle) MouseUp(button int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.MouseUp(ctx, button)
}

// ObservePointer performs the scoped ObservePointer operation.
func (h InputBundle) ObservePointer() (r0 input.PointerObservation, err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.ObservePointer(ctx)
}

// PointerCoordinateSpace performs the scoped PointerCoordinateSpace operation.
func (h InputBundle) PointerCoordinateSpace() (r0 input.CoordinateSpaceInfo, err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.PointerCoordinateSpace(ctx)
}

// PointerLocation performs the scoped PointerLocation operation.
func (h InputBundle) PointerLocation() (r0 int, r1 int, err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.PointerLocation(ctx)
}

// ScrollDown performs the scoped ScrollDown operation.
func (h InputBundle) ScrollDown(clicks int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.ScrollDown(ctx, clicks)
}

// ScrollLeft performs the scoped ScrollLeft operation.
func (h InputBundle) ScrollLeft(clicks int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.ScrollLeft(ctx, clicks)
}

// ScrollRight performs the scoped ScrollRight operation.
func (h InputBundle) ScrollRight(clicks int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.ScrollRight(ctx, clicks)
}

// ScrollUp performs the scoped ScrollUp operation.
func (h InputBundle) ScrollUp(clicks int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.ScrollUp(ctx, clicks)
}

// Sync performs the scoped Sync operation.
func (h InputBundle) Sync() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.Sync(ctx)
}

// SendKeys performs the scoped Type operation.
func (h InputBundle) SendKeys(text string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.Type(ctx, text)
}

// Type performs the scoped TypeLiteral operation.
func (h InputBundle) Type(text string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.TypeLiteral(ctx, text)
}

// WithContext returns a copy bound to ctx without changing the original.
func (h InputObservationBundle) WithContext(ctx context.Context) InputObservationBundle {
	h.scope = h.scope.withContext(ctx)
	return h
}

// WithTimeout returns a copy with an operation timeout override.
func (h InputObservationBundle) WithTimeout(d time.Duration) InputObservationBundle {
	h.scope = h.scope.withTimeout(d)
	return h
}

// KeyState performs the scoped KeyState operation.
func (h InputObservationBundle) KeyState(name string) (r0 input.StateObservation, err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.KeyState(ctx, name)
}

// Listen performs the scoped Listen operation.
func (h InputObservationBundle) Listen(f input.ObservationFilter) (r0 input.ObservationSubscription, err error) {
	setup, err := h.scope.startStream()
	if err != nil {
		return nil, err
	}
	sub, err := h.handle.Listen(setup.ctx, f)
	if setupErr := setup.finish(); setupErr != nil {
		err = errors.Join(err, setupErr)
	}
	if err != nil {
		if sub != nil {
			err = errors.Join(err, sub.Close())
		}
		setup.release()
		return nil, err
	}
	if h.scope.timeout > 0 {
		return newScopedObservation(sub, setup), nil
	}
	return sub, nil
}

// LockState performs the scoped LockState operation.
func (h InputObservationBundle) LockState(name string) (r0 input.StateObservation, err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.LockState(ctx, name)
}

// MouseButtonState performs the scoped MouseButtonState operation.
func (h InputObservationBundle) MouseButtonState(button int) (r0 input.StateObservation, err error) {
	ctx, finish, err := h.operationScope().begin(scopeShort, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.handle.MouseButtonState(ctx, button)
}

// WithContext returns a copy bound to ctx without changing the original.
func (h ScreenBundle) WithContext(ctx context.Context) ScreenBundle {
	h.scope = h.scope.withContext(ctx)
	return h
}

// WithTimeout returns a copy with an operation timeout override.
func (h ScreenBundle) WithTimeout(d time.Duration) ScreenBundle {
	h.scope = h.scope.withTimeout(d)
	return h
}

// Capture performs the scoped Capture operation.
func (h ScreenBundle) Capture(rect ...image.Rectangle) (r0 screen.Capture, err error) {
	if len(rect) > 1 {
		err = ErrInvalidArgument
		return
	}
	var region image.Rectangle
	if len(rect) == 1 {
		region = rect[0]
	}
	if len(rect) == 0 {
		err = fmt.Errorf("screen: whole-desktop provenance unavailable; use Grab() for an image or Capture(rect) for an explicit region: %w", ErrUnsupported)
		return
	}
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().Capture(ctx, region)
}

// CaptureRegion performs the scoped CaptureRegion operation.
func (h ScreenBundle) CaptureRegion(rect image.Rectangle, path string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().CaptureRegion(ctx, rect, path)
}

// CaptureWindow captures the current screen rectangle reported by a stable
// Session-owned window handle. It captures the visible desktop composite in
// that rectangle, including any windows drawn above the target; it does not
// retrieve hidden or minimized window contents.
func (h ScreenBundle) CaptureWindow(target *Window) (r0 screen.WindowCapture, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.state.CaptureWindow(ctx, target.privateState())
}

// CaptureWindowBackground reads a target-only buffer when the selected screen
// backend exposes that capability. It never activates the target and never
// falls back to a visible desktop crop.
func (h ScreenBundle) CaptureWindowBackground(target *Window) (r0 screen.WindowCapture, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.state.CaptureWindowBackground(ctx, target.privateState())
}

// CaptureWindowBackgroundTo captures one target-only background buffer and
// atomically publishes it as PNG at path.
func (h ScreenBundle) CaptureWindowBackgroundTo(target *Window, path string) (r0 screen.WindowCapture, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.state.CaptureWindowBackgroundTo(ctx, target.privateState(), path)
}

// CaptureWindowTo captures one stable window's visible screen rectangle and
// atomically publishes it as PNG at path. The captured content is the desktop
// composite, not a background-only buffer.
func (h ScreenBundle) CaptureWindowTo(target *Window, path string) (r0 screen.WindowCapture, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.state.CaptureWindowTo(ctx, target.privateState(), path)
}

// FindColor performs the scoped FindColor operation.
func (h ScreenBundle) FindColor(rect image.Rectangle, target color.RGBA, tolerance int) (r0 image.Point, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().FindColor(ctx, rect, target, tolerance)
}

// FindTemplate performs the scoped FindTemplate operation.
func (h ScreenBundle) FindTemplate(rect image.Rectangle, reference image.Image, minConfidence float64) (r0 find.TemplateMatch, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().FindTemplate(ctx, rect, reference, minConfidence)
}

// FindTemplates performs the scoped FindTemplates operation.
func (h ScreenBundle) FindTemplates(rect image.Rectangle, reference image.Image, minConfidence float64, maxResults int) (r0 []find.TemplateMatch, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().FindTemplates(ctx, rect, reference, minConfidence, maxResults)
}

// FindText performs the scoped FindText operation.
func (h ScreenBundle) FindText(rect image.Rectangle, query string, options find.TextOptions) (r0 find.TextResult, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().FindText(ctx, rect, query, options)
}

// GetMultiplePixels performs the scoped GetMultiplePixels operation.
func (h ScreenBundle) GetMultiplePixels(points []image.Point) (r0 []color.RGBA, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().GetMultiplePixels(ctx, points)
}

// GetPixel performs the scoped GetPixel operation.
func (h ScreenBundle) GetPixel(x int, y int) (r0 color.RGBA, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().GetPixel(ctx, x, y)
}

// Grab performs the scoped Grab operation.
func (h ScreenBundle) Grab(rect ...image.Rectangle) (r0 image.Image, err error) {
	if len(rect) > 1 {
		err = ErrInvalidArgument
		return
	}
	var region image.Rectangle
	if len(rect) == 1 {
		region = rect[0]
	}
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().Grab(ctx, region)
}

// GrabFullHash performs the scoped GrabFullHash operation.
func (h ScreenBundle) GrabFullHash() (r0 uint32, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().GrabFullHash(ctx)
}

// GrabRegionHash performs the scoped GrabRegionHash operation.
func (h ScreenBundle) GrabRegionHash(rect image.Rectangle) (r0 uint32, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().GrabRegionHash(ctx, rect)
}

// Resolution performs the scoped Resolution operation.
func (h ScreenBundle) Resolution() (r0 int, r1 int, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().Resolution(ctx)
}

// ScanFor performs the scoped ScanFor operation.
func (h ScreenBundle) ScanFor(rects []image.Rectangle, wants []uint32, poll time.Duration) (r0 find.Result, err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().ScanFor(ctx, rects, wants, poll)
}

// WaitFor performs the scoped WaitFor operation.
func (h ScreenBundle) WaitFor(rect image.Rectangle, want uint32, poll time.Duration) (r0 uint32, err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().WaitFor(ctx, rect, want, poll)
}

// WaitForChange performs the scoped WaitForChange operation.
func (h ScreenBundle) WaitForChange(rect image.Rectangle, initial uint32, poll time.Duration) (r0 uint32, err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().WaitForChange(ctx, rect, initial, poll)
}

// WaitForFn performs the scoped WaitForFn operation.
func (h ScreenBundle) WaitForFn(rect image.Rectangle, fn func(context.Context, image.Image) bool, poll time.Duration) (r0 image.Image, err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().WaitForFn(ctx, rect, fn, poll)
}

// WaitForNoChange performs the scoped WaitForNoChange operation.
func (h ScreenBundle) WaitForNoChange(rect image.Rectangle, stable int, poll time.Duration) (r0 uint32, err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().WaitForNoChange(ctx, rect, stable, poll)
}

// WaitForNoChangeFrom performs the scoped WaitForNoChangeFrom operation.
func (h ScreenBundle) WaitForNoChangeFrom(rect image.Rectangle, initial uint32, stable int, poll time.Duration) (r0 uint32, err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().WaitForNoChangeFrom(ctx, rect, initial, stable, poll)
}

// WaitForSettle performs the scoped WaitForSettle operation.
func (h ScreenBundle) WaitForSettle(rect image.Rectangle, action func() error, stable int, poll time.Duration) (r0 uint32, err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().WaitForSettle(ctx, rect, action, stable, poll)
}

// WithContext returns a copy bound to ctx without changing the original.
func (h WindowBundle) WithContext(ctx context.Context) WindowBundle {
	h.scope = h.scope.withContext(ctx)
	return h
}

// WithTimeout returns a copy with an operation timeout override.
func (h WindowBundle) WithTimeout(d time.Duration) WindowBundle {
	h.scope = h.scope.withTimeout(d)
	return h
}

// ActiveTitle performs the scoped ActiveTitle operation.
func (h WindowBundle) ActiveTitle() (r0 string, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().ActiveTitle(ctx)
}

// CursorPosition performs the scoped CursorPosition operation.
func (h WindowBundle) CursorPosition() (r0 int, r1 int, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().CursorPosition(ctx)
}

// Events performs the scoped Events operation.
func (h WindowBundle) Events() (r0 <-chan window.Event, err error) {
	setup, err := h.scope.startStream()
	if err != nil {
		return nil, err
	}
	stream, err := h.backendHandle().Events()
	if setupErr := setup.finish(); setupErr != nil {
		err = errors.Join(err, setupErr)
	}
	if err != nil {
		setup.release()
		return nil, err
	}
	if stream == nil {
		setup.release()
		return nil, ErrUnsupported
	}
	// All public subscribers use the same session-owned reader. The provider
	// channel is never returned alongside a competing forwarding goroutine.
	if h.scope.core == nil || h.scope.core.ctx == nil {
		setup.release()
		return nil, ErrNilSession
	}
	ctx, cancel := context.WithCancel(setup.ctx)
	stop := context.AfterFunc(h.scope.core.ctx, cancel)
	release := sync.OnceFunc(func() { stop(); cancel(); setup.release() })
	return h.state.events.subscribe(ctx, h.scope.core.ctx, stream, release)
}

// Find performs the scoped Find operation.
func (h WindowBundle) Find(match window.Match) (r0 *Window, err error) {
	if validationErr := match.Validate(); validationErr != nil {
		return nil, errors.Join(ErrInvalidArgument, validationErr)
	}
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.findContext(ctx, match)
}

// FloatWindowByID performs the scoped FloatWindowByID operation.
func (h WindowBundle) FloatWindowByID(id string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().FloatWindowByID(ctx, id)
}

// List performs the scoped List operation.
func (h WindowBundle) List(match ...window.Match) (r0 []*Window, err error) {
	if len(match) > 1 {
		err = ErrInvalidArgument
		return
	}
	var selector window.Match
	if len(match) == 1 {
		selector = match[0]
		if validationErr := selector.Validate(); validationErr != nil {
			return nil, errors.Join(ErrInvalidArgument, validationErr)
		}
	}
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.listContext(ctx, selector)
}

// ListDesktops performs the scoped ListDesktops operation.
func (h WindowBundle) ListDesktops() (r0 []window.DesktopInfo, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().ListDesktops(ctx)
}

// SetKeepAboveByID performs the scoped SetKeepAboveByID operation.
func (h WindowBundle) SetKeepAboveByID(id string, on bool) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SetKeepAboveByID(ctx, id, on)
}

// SetKeepBelowByID performs the scoped SetKeepBelowByID operation.
func (h WindowBundle) SetKeepBelowByID(id string, on bool) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SetKeepBelowByID(ctx, id, on)
}

// SetShadeByID performs the scoped SetShadeByID operation.
func (h WindowBundle) SetShadeByID(id string, on bool) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SetShadeByID(ctx, id, on)
}

// SetWindowDesktopByID performs the scoped SetWindowDesktopByID operation.
func (h WindowBundle) SetWindowDesktopByID(id string, desktopID string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SetWindowDesktopByID(ctx, id, desktopID)
}

// SwitchDesktop performs the scoped SwitchDesktop operation.
func (h WindowBundle) SwitchDesktop(id string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SwitchDesktop(ctx, id)
}

// Sync performs the scoped Sync operation.
func (h WindowBundle) Sync() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().Sync(ctx)
}

// TileWindowByID performs the scoped TileWindowByID operation.
func (h WindowBundle) TileWindowByID(id string, x int, y int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().TileWindowByID(ctx, id, x, y)
}

// Wait performs the scoped Wait operation.
func (h WindowBundle) Wait(match window.Match, options ...WaitOption) (r0 *Window, err error) {
	if validationErr := match.Validate(); validationErr != nil {
		return nil, errors.Join(ErrInvalidArgument, validationErr)
	}
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.waitContext(ctx, match, options...)
}

// WithContext returns a copy bound to ctx without changing the original.
func (h OutputBundle) WithContext(ctx context.Context) OutputBundle {
	h.scope = h.scope.withContext(ctx)
	return h
}

// WithTimeout returns a copy with an operation timeout override.
func (h OutputBundle) WithTimeout(d time.Duration) OutputBundle {
	h.scope = h.scope.withTimeout(d)
	return h
}

// List returns the displays visible to the active output backend.
func (h OutputBundle) List() (r0 []output.Info, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.state.List(ctx)
}

// WithContext returns a copy bound to ctx without changing the original.
func (h ClipboardBundle) WithContext(ctx context.Context) ClipboardBundle {
	h.scope = h.scope.withContext(ctx)
	return h
}

// WithTimeout returns a copy with an operation timeout override.
func (h ClipboardBundle) WithTimeout(d time.Duration) ClipboardBundle {
	h.scope = h.scope.withTimeout(d)
	return h
}

// Get returns the current clipboard text.
func (h ClipboardBundle) Get() (r0 string, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.state.Get(ctx)
}

// Set replaces the clipboard text.
func (h ClipboardBundle) Set(text string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.state.Set(ctx, text)
}

// WithContext returns a copy bound to ctx without changing the original.
func (h AccessibilityBundle) WithContext(ctx context.Context) AccessibilityBundle {
	h.scope = h.scope.withContext(ctx)
	return h
}

// WithTimeout returns a copy with an operation timeout override.
func (h AccessibilityBundle) WithTimeout(d time.Duration) AccessibilityBundle {
	h.scope = h.scope.withTimeout(d)
	return h
}

// AccessibilityWindow performs the scoped AccessibilityWindow operation.
func (h AccessibilityBundle) AccessibilityWindow(target accessibility.WindowTarget) (r0 accessibility.WindowScope, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().AccessibilityWindow(ctx, target)
}

// AddTextSelection performs the scoped AddTextSelection operation.
func (h AccessibilityBundle) AddTextSelection(id accessibility.NodeID, start int32, end int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().AddTextSelection(ctx, id, start, end)
}

// Applications performs the scoped Applications operation.
func (h AccessibilityBundle) Applications() (r0 []accessibility.Application, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().Applications(ctx)
}

// AtPoint performs the scoped AtPoint operation.
func (h AccessibilityBundle) AtPoint(x int, y int) (r0 accessibility.Node, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().AtPoint(ctx, x, y)
}

// ClearSelection performs the scoped ClearSelection operation.
func (h AccessibilityBundle) ClearSelection(id accessibility.NodeID) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().ClearSelection(ctx, id)
}

// CopyText performs the scoped CopyText operation.
func (h AccessibilityBundle) CopyText(id accessibility.NodeID, start int32, end int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().CopyText(ctx, id, start, end)
}

// CutText performs the scoped CutText operation.
func (h AccessibilityBundle) CutText(id accessibility.NodeID, start int32, end int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().CutText(ctx, id, start, end)
}

// DeleteText performs the scoped DeleteText operation.
func (h AccessibilityBundle) DeleteText(id accessibility.NodeID, start int32, end int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().DeleteText(ctx, id, start, end)
}

// DeselectChild performs the scoped DeselectChild operation.
func (h AccessibilityBundle) DeselectChild(id accessibility.NodeID, index int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().DeselectChild(ctx, id, index)
}

// DeselectColumn performs the scoped DeselectColumn operation.
func (h AccessibilityBundle) DeselectColumn(id accessibility.NodeID, column int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().DeselectColumn(ctx, id, column)
}

// DeselectRow performs the scoped DeselectRow operation.
func (h AccessibilityBundle) DeselectRow(id accessibility.NodeID, row int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().DeselectRow(ctx, id, row)
}

// DeselectSelectedChild performs the scoped DeselectSelectedChild operation.
func (h AccessibilityBundle) DeselectSelectedChild(id accessibility.NodeID) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().DeselectSelectedChild(ctx, id)
}

// Events performs the scoped Events operation.
func (h AccessibilityBundle) Events(opts accessibility.EventOptions) (r0 <-chan accessibility.Event, err error) {
	setup, err := h.scope.startStream()
	if err != nil {
		return nil, err
	}
	stream, err := h.backendHandle().Events(setup.ctx, opts)
	if setupErr := setup.finish(); setupErr != nil {
		err = errors.Join(err, setupErr)
	}
	if err != nil {
		setup.release()
		return nil, err
	}
	if h.scope.timeout > 0 {
		return scopedEvents(setup.ctx, stream, setup.release), nil
	}
	return stream, nil
}

// Find performs the scoped Find operation.
func (h AccessibilityBundle) Find(root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) (r0 []accessibility.Node, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().Find(ctx, root, query, opts)
}

// FindApplication resolves a unique application in the owning session.
func (h AccessibilityBundle) FindApplication(filter accessibility.ApplicationFilter) (r0 accessibility.Application, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.state.FindApplication(ctx, filter)
}

// FindApplicationFresh resolves an application from uncached identity state.
func (h AccessibilityBundle) FindApplicationFresh(filter accessibility.ApplicationFilter) (r0 accessibility.Application, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.state.FindApplicationFresh(ctx, filter)
}

// FindOne performs the scoped FindOne operation.
func (h AccessibilityBundle) FindOne(root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) (r0 accessibility.Node, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().FindOne(ctx, root, query, opts)
}

// FindOneSelector performs the scoped FindOneSelector operation.
func (h AccessibilityBundle) FindOneSelector(root accessibility.NodeID, selector accessibility.Selector, opts accessibility.SnapshotOptions) (r0 accessibility.Node, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().FindOneSelector(ctx, root, selector, opts)
}

// FocusNode performs the scoped FocusNode operation.
func (h AccessibilityBundle) FocusNode(id accessibility.NodeID) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().FocusNode(ctx, id)
}

// Focused performs the scoped Focused operation.
func (h AccessibilityBundle) Focused(opts accessibility.SnapshotOptions) (r0 accessibility.Node, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().Focused(ctx, opts)
}

// Generation performs the scoped Generation operation.
func (h AccessibilityBundle) Generation() (r0 uint64) {
	return h.backendHandle().Generation()
}

// InsertText performs the scoped InsertText operation.
func (h AccessibilityBundle) InsertText(id accessibility.NodeID, offset int32, value string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().InsertText(ctx, id, offset, value)
}

// Invalidate performs the scoped Invalidate operation.
func (h AccessibilityBundle) Invalidate(id accessibility.NodeID) {
	h.backendHandle().Invalidate(id)
}

// InvokeAction performs the scoped InvokeAction operation.
func (h AccessibilityBundle) InvokeAction(id accessibility.NodeID, index int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().InvokeAction(ctx, id, index)
}

// InvokeActionByName performs the scoped InvokeActionByName operation.
func (h AccessibilityBundle) InvokeActionByName(id accessibility.NodeID, name string) (r0 accessibility.Action, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().InvokeActionByName(ctx, id, name)
}

// InvokeDefaultAction performs the scoped InvokeDefaultAction operation.
func (h AccessibilityBundle) InvokeDefaultAction(id accessibility.NodeID) (r0 accessibility.Action, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().InvokeDefaultAction(ctx, id)
}

// InvokeSelectorSemanticAction performs the scoped InvokeSelectorSemanticAction operation.
func (h AccessibilityBundle) InvokeSelectorSemanticAction(root accessibility.NodeID, selector accessibility.Selector, actionName string, opts accessibility.SnapshotOptions) (r0 accessibility.ActionReceipt, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().InvokeSelectorSemanticAction(ctx, root, selector, actionName, opts)
}

// InvokeSemanticAction performs the scoped InvokeSemanticAction operation.
func (h AccessibilityBundle) InvokeSemanticAction(root accessibility.NodeID, query accessibility.Query, actionName string, opts accessibility.SnapshotOptions) (r0 accessibility.ActionReceipt, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().InvokeSemanticAction(ctx, root, query, actionName, opts)
}

// Outline performs the scoped Outline operation.
func (h AccessibilityBundle) Outline(root accessibility.NodeID, snapshotOptions accessibility.SnapshotOptions, outlineOptions accessibility.OutlineOptions) (r0 accessibility.Outline, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().Outline(ctx, root, snapshotOptions, outlineOptions)
}

// PasteText performs the scoped PasteText operation.
func (h AccessibilityBundle) PasteText(id accessibility.NodeID, position int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().PasteText(ctx, id, position)
}

// RemoveTextSelection performs the scoped RemoveTextSelection operation.
func (h AccessibilityBundle) RemoveTextSelection(id accessibility.NodeID, selection int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().RemoveTextSelection(ctx, id, selection)
}

// Reopen performs the scoped Reopen operation.
func (h AccessibilityBundle) Reopen() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().Reopen(ctx)
}

// ReplaceEditableText performs the scoped ReplaceEditableText operation.
func (h AccessibilityBundle) ReplaceEditableText(id accessibility.NodeID, value string) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().ReplaceEditableText(ctx, id, value)
}

// ScrollNodeIntoView performs the scoped ScrollNodeIntoView operation.
func (h AccessibilityBundle) ScrollNodeIntoView(id accessibility.NodeID) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().ScrollNodeIntoView(ctx, id)
}

// ScrollTo performs the scoped ScrollTo operation.
func (h AccessibilityBundle) ScrollTo(id accessibility.NodeID, kind accessibility.ScrollType) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().ScrollTo(ctx, id, kind)
}

// ScrollToPoint performs the scoped ScrollToPoint operation.
func (h AccessibilityBundle) ScrollToPoint(id accessibility.NodeID, kind accessibility.CoordType, x int, y int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().ScrollToPoint(ctx, id, kind, x, y)
}

// SelectAll performs the scoped SelectAll operation.
func (h AccessibilityBundle) SelectAll(id accessibility.NodeID) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SelectAll(ctx, id)
}

// SelectChild performs the scoped SelectChild operation.
func (h AccessibilityBundle) SelectChild(id accessibility.NodeID, index int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SelectChild(ctx, id, index)
}

// SelectColumn performs the scoped SelectColumn operation.
func (h AccessibilityBundle) SelectColumn(id accessibility.NodeID, column int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SelectColumn(ctx, id, column)
}

// SelectRow performs the scoped SelectRow operation.
func (h AccessibilityBundle) SelectRow(id accessibility.NodeID, row int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SelectRow(ctx, id, row)
}

// SetCaretOffset performs the scoped SetCaretOffset operation.
func (h AccessibilityBundle) SetCaretOffset(id accessibility.NodeID, offset int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SetCaretOffset(ctx, id, offset)
}

// SetExtents performs the scoped SetExtents operation.
func (h AccessibilityBundle) SetExtents(id accessibility.NodeID, x int, y int, width int, height int, kind accessibility.CoordType) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SetExtents(ctx, id, x, y, width, height, kind)
}

// SetPosition performs the scoped SetPosition operation.
func (h AccessibilityBundle) SetPosition(id accessibility.NodeID, x int, y int, kind accessibility.CoordType) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SetPosition(ctx, id, x, y, kind)
}

// SetSize performs the scoped SetSize operation.
func (h AccessibilityBundle) SetSize(id accessibility.NodeID, width int, height int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SetSize(ctx, id, width, height)
}

// SetTextSelection performs the scoped SetTextSelection operation.
func (h AccessibilityBundle) SetTextSelection(id accessibility.NodeID, selection int32, start int32, end int32) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SetTextSelection(ctx, id, selection, start, end)
}

// SetTextSelections performs the scoped SetTextSelections operation.
func (h AccessibilityBundle) SetTextSelections(id accessibility.NodeID, selections []accessibility.DocumentTextSelection) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SetTextSelections(ctx, id, selections)
}

// SetValue performs the scoped SetValue operation.
func (h AccessibilityBundle) SetValue(id accessibility.NodeID, value float64) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().SetValue(ctx, id, value)
}

// Snapshot performs the scoped Snapshot operation.
func (h AccessibilityBundle) Snapshot(root accessibility.NodeID, opts accessibility.SnapshotOptions) (r0 accessibility.Snapshot, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.backendHandle().Snapshot(ctx, root, opts)
}

// WindowRoot resolves a managed window's exact accessibility subtree.
func (h AccessibilityBundle) WindowRoot(id string) (r0 accessibility.WindowScope, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.state.WindowRoot(ctx, id)
}

// WindowRootFresh re-correlates a managed window from fresh state.
func (h AccessibilityBundle) WindowRootFresh(id string) (r0 accessibility.WindowScope, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.state.WindowRootFresh(ctx, id)
}

// WithContext returns a copy bound to ctx without changing the original.
func (h *Window) WithContext(ctx context.Context) *Window {
	if h == nil {
		h = &Window{}
	}
	copy := *h
	copy.scope = copy.scope.withContext(ctx)
	return &copy
}

// WithTimeout returns a copy with an operation timeout override.
func (h *Window) WithTimeout(d time.Duration) *Window {
	if h == nil {
		h = &Window{}
	}
	copy := *h
	copy.scope = copy.scope.withTimeout(d)
	return &copy
}

// Activate focuses the window.
func (h *Window) Activate() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().Activate(ctx)
}

// Close requests that the window close.
func (h *Window) Close() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().Close(ctx)
}

// Fullscreen makes the window occupy the screen.
func (h *Window) Fullscreen() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().Fullscreen(ctx)
}

// Info refreshes and returns authoritative window state.
func (h *Window) Info() (r0 window.Info, err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().Info(ctx)
}

// Maximize maximizes the window.
func (h *Window) Maximize() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().Maximize(ctx)
}

// Minimize minimizes the window.
func (h *Window) Minimize() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().Minimize(ctx)
}

// Move positions the window at x and y. Negative coordinates are valid for a
// monitor left of or above the primary one; a coordinate no backend can carry
// is rejected with ErrInvalidArgument.
func (h *Window) Move(x int, y int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().Move(ctx, x, y)
}

// Resize changes the window size to width by height. Both edges must be
// positive and within the geometry range every backend can represent; anything
// else is rejected with ErrInvalidArgument rather than sent to a compositor that
// would truncate it.
func (h *Window) Resize(width int, height int) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().Resize(ctx, width, height)
}

// Restore returns the window to its normal state.
func (h *Window) Restore() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().Restore(ctx)
}

// Unfullscreen restores the window from fullscreen mode.
func (h *Window) Unfullscreen() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeMedium, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().Unfullscreen(ctx)
}

// WaitClosed waits until the authoritative backend no longer reports w.
func (h *Window) WaitClosed(options ...WaitOption) (err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().WaitClosed(ctx, options...)
}

// WithContext returns a copy bound to ctx without changing the original.
func (h *Application) WithContext(ctx context.Context) *Application {
	if h == nil {
		h = &Application{}
	}
	copy := *h
	copy.scope = copy.scope.withContext(ctx)
	return &copy
}

// WithTimeout returns a copy with an operation timeout override.
func (h *Application) WithTimeout(d time.Duration) *Application {
	if h == nil {
		h = &Application{}
	}
	copy := *h
	copy.scope = copy.scope.withTimeout(d)
	return &copy
}

// Stop sends SIGTERM to the application process group and waits for ctx.
func (h *Application) Stop() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeCleanup, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.privateState().Stop(ctx)
}

// Wait performs the scoped Wait operation.
func (h *Application) Wait() (err error) {
	ctx, finish, err := h.operationScope().begin(scopeCleanup, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.waitContext(ctx)
}

// WaitForWindow performs the scoped WaitForWindow operation.
func (h *Application) WaitForWindow(match window.Match) (r0 *Window, err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.waitForWindowContext(ctx, match)
}

// WithContext returns a copy bound to ctx without changing the original.
func (h *AccessibilityLocator) WithContext(ctx context.Context) *AccessibilityLocator {
	if h == nil {
		h = &AccessibilityLocator{}
	}
	copy := *h
	copy.operation = copy.operation.withContext(ctx)
	return &copy
}

// WithTimeout returns a copy with an operation timeout override.
func (h *AccessibilityLocator) WithTimeout(d time.Duration) *AccessibilityLocator {
	if h == nil {
		h = &AccessibilityLocator{}
	}
	copy := *h
	copy.operation = copy.operation.withTimeout(d)
	return &copy
}

// ClickAndWait performs the scoped ClickAndWait operation.
func (h *AccessibilityLocator) ClickAndWait(postcondition Condition, options ...WaitOption) (r0 accessibility.ActionReceipt, r1 WaitEvidence, err error) {
	ctx, finish, err := h.operationScope().begin(scopeBatch, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.clickAndWaitContext(ctx, postcondition, options...)
}

// FillAndWait performs the scoped FillAndWait operation.
func (h *AccessibilityLocator) FillAndWait(value string, postcondition Condition, options ...WaitOption) (r0 accessibility.ActionReceipt, r1 WaitEvidence, err error) {
	ctx, finish, err := h.operationScope().begin(scopeBatch, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.fillContext(ctx, value, true, postcondition, options...)
}

// InvokeActionAndWait performs the scoped InvokeActionAndWait operation.
func (h *AccessibilityLocator) InvokeActionAndWait(actionName string, postcondition Condition, options ...WaitOption) (r0 accessibility.ActionReceipt, r1 WaitEvidence, err error) {
	ctx, finish, err := h.operationScope().begin(scopeBatch, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.invokeActionContext(ctx, actionName, true, postcondition, options...)
}

// Matches performs the scoped Matches operation.
func (h *AccessibilityLocator) Matches() (r0 []accessibility.Node, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.matchesContext(ctx)
}

// Resolve performs the scoped Resolve operation.
func (h *AccessibilityLocator) Resolve() (r0 accessibility.Node, err error) {
	ctx, finish, err := h.operationScope().begin(scopeLong, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.resolveContext(ctx)
}

// Wait performs the scoped Wait operation.
func (h *AccessibilityLocator) Wait(options ...WaitOption) (r0 accessibility.Node, r1 WaitEvidence, err error) {
	ctx, finish, err := h.operationScope().begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.waitContext(ctx, options...)
}

// WaitForState performs the scoped WaitForState operation.
func (h *AccessibilityLocator) WaitForState(state AccessibilityWaitState, timeout time.Duration, options ...WaitOption) (r0 accessibility.Node, r1 WaitEvidence, err error) {
	scope := h.operationScope()
	if scope.timeout == 0 && timeout > 0 {
		scope = scope.withTimeout(timeout)
	}
	ctx, finish, err := scope.begin(scopeWait, 0)
	if err != nil {
		return
	}
	defer finish(&err)
	return h.waitForStateContext(ctx, state, timeout, options...)
}
