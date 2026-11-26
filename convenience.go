package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/keymap"
	"github.com/nskaggs/perfuncted/v2/window"
	"strings"
	"time"
)

// OpenDefault opens a session requiring input and no other capabilities by
// default. Explicit optional input remains optional. Startup uses the selected
// target's startup timeout. Wayland authorization may be requested at startup.
func openDefault(opts ...Option) (*sessionCore, error) {
	cfg := openConfig{required: make(map[Capability]struct{}), optional: make(map[Capability]struct{}), target: targetSelection{kind: TargetHost}}
	for _, opt := range opts {
		if opt != nil {
			if err := opt(&cfg); err != nil {
				return nil, err
			}
		}
	}
	if _, required := cfg.required[CapabilityInput]; !required {
		if _, optional := cfg.optional[CapabilityInput]; !optional {
			cfg.required[CapabilityInput] = struct{}{}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.target.config.Timeouts.WithDefaults().Startup)
	defer cancel()
	return openSession(ctx, func(dst *openConfig) error { *dst = cfg; return nil })
}

// SessionView holds immutable operation settings for a Session. It does not
// own the session; closing the Session cancels operations through the view.
type SessionView struct {
	session *sessionCore
	ctx     context.Context //nolint:containedctx // The view explicitly carries caller context.
	timeout time.Duration
}

// WithContext returns a non-owning view. A nil context is rejected on use.
func (s *sessionCore) WithContext(ctx context.Context) *SessionView {
	return &SessionView{session: s, ctx: ctx}
}

// WithContext returns a copy of the view with the given context.
func (v *SessionView) WithContext(ctx context.Context) *SessionView {
	if v == nil {
		return &SessionView{ctx: ctx}
	}
	copy := *v
	copy.ctx = ctx
	return &copy
}

// WithTimeout returns a copy with a batch timeout. Zero adds no batch deadline;
// individual input operations still use the session policy. Negative values
// are rejected on use.
func (v *SessionView) WithTimeout(d time.Duration) *SessionView {
	if v == nil {
		return &SessionView{timeout: d}
	}
	copy := *v
	copy.timeout = d
	return &copy
}

// WithTimeout returns a non-owning view with a batch timeout.
func (s *sessionCore) WithTimeout(d time.Duration) *SessionView {
	return &SessionView{session: s, ctx: context.Background(), timeout: d}
}

// Step is an immutable description of an input operation or delay.
type Step struct {
	kind  string
	text  string
	x, y  int
	delay time.Duration
}

// Type constructs a literal-text step; braces and modifiers are not parsed.
func Type(text string) Step { return Step{kind: "type-literal", text: text} }

// Key constructs a single named-key step using the portable key vocabulary.
// TypeKeys is available for backend-specific key names.
func Key(name string) Step { return Step{kind: "key", text: name} }

// Hotkey constructs a chord with modifiers followed by a key.
func Hotkey(keys ...string) Step { return Step{kind: "hotkey", text: strings.Join(keys, "+")} }

// TypeKeys constructs a step using the input backend's explicit key syntax.
func TypeKeys(sequence string) Step { return Step{kind: "type-keys", text: sequence} }

// Click constructs a primary-button click at desktop coordinates.
func Click(x, y int) Step { return Step{kind: "click", x: x, y: y} }

// Delay constructs a cancellable pause between steps.
func Delay(d time.Duration) Step { return Step{kind: "delay", delay: d} }

// StepError identifies a failed step by its zero-based index without recording
// the step's input text. The underlying cause is available via Unwrap.
type StepError struct {
	// Index is the zero-based position of the failed step.
	Index int
	// Operation names the step without exposing its arguments.
	Operation string
	// Err retains the underlying failure.
	Err error
	// MayHaveDispatched reports that input may have reached the desktop.
	MayHaveDispatched bool
}

func (e *StepError) Error() string {
	category := "operation failed"
	switch {
	case errors.Is(e.Err, ErrInvalidArgument):
		category = "invalid argument"
	case errors.Is(e.Err, ErrUnavailable):
		category = "capability unavailable"
	case errors.Is(e.Err, ErrUnsupported):
		category = "operation unsupported"
	case errors.Is(e.Err, ErrSessionClosed):
		category = "session closed"
	case errors.Is(e.Err, ErrWindowFocusUnverified):
		category = "window focus unverified"
	case errors.Is(e.Err, ErrWindowIdentityChanged):
		category = "selected window changed"
	case errors.Is(e.Err, context.Canceled):
		category = "canceled"
	case errors.Is(e.Err, context.DeadlineExceeded):
		category = "deadline exceeded"
	}
	message := fmt.Sprintf("step %d (%s): %s", e.Index+1, e.Operation, category)
	if e.MayHaveDispatched {
		message += "; input may have been dispatched"
	}
	return message
}
func (e *StepError) Unwrap() error { return e.Err }

func validToken(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	_, known := keymap.FromString(s)
	return known
}
func (step Step) validate(ctx context.Context) error {
	switch step.kind {
	case "type-literal", "click":
		return nil
	case "type-keys":
		if input.ValidateKeySequence(ctx, step.text) == nil {
			return nil
		}
	case "delay":
		if step.delay >= 0 {
			return nil
		}
	case "key":
		if validToken(step.text) {
			return nil
		}
	case "hotkey":
		return validateHotkey(ctx, step.text)
	}
	return ErrInvalidArgument
}

func validateHotkey(ctx context.Context, chord string) error {
	if len(chord) > 256 {
		return ErrInvalidArgument
	}
	parts := strings.Split(chord, "+")
	if len(parts) < 2 || len(parts) > 5 || !validToken(parts[len(parts)-1]) {
		return ErrInvalidArgument
	}
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(p) {
		case "ctrl", "control", "alt", "shift", "super", "meta", "win", "logo":
		default:
			return ErrInvalidArgument
		}
	}
	if input.ValidateKeySequence(ctx, "{"+chord+"}") != nil {
		return ErrInvalidArgument
	}
	return nil
}

// Do executes steps in order against the currently focused application. It
// preflights known requirements but cannot guarantee application acceptance or
// atomic focus, and does not retry failed input.
func (s *sessionCore) Do(steps ...Step) error {
	return (&SessionView{session: s, ctx: context.Background()}).Do(steps...)
}

// Type immediately sends literal text to the currently focused application.
func (s *sessionCore) Type(text string) error { return s.Do(Type(text)) }

// Key immediately sends one named key to the currently focused application.
func (s *sessionCore) Key(name string) error { return s.Do(Key(name)) }

// Type immediately sends literal text using the view's context and timeout.
func (v *SessionView) Type(text string) error { return v.Do(Type(text)) }

// Key immediately sends one named key using the view's context and timeout.
func (v *SessionView) Key(name string) error { return v.Do(Key(name)) }

// Do executes steps under the view's context, timeout and session lifetime.
func (v *SessionView) operationContext() (context.Context, func(), error) {
	if v == nil || v.session == nil {
		return nil, nil, ErrNilSession
	}
	if err := v.session.ensureOpen(); err != nil {
		return nil, nil, err
	}
	if v.ctx == nil || v.timeout < 0 {
		return nil, nil, ErrInvalidArgument
	}
	if err := v.ctx.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(v.ctx)
	stop := context.AfterFunc(v.session.ctx, cancel)
	cleanup := func() { stop(); cancel() }
	if v.timeout > 0 {
		bounded, cancelTimeout := context.WithTimeout(ctx, v.timeout)
		return bounded, func() { cancelTimeout(); cleanup() }, nil
	}
	return ctx, cleanup, nil
}

// Do executes the steps after a single shared capability preflight.
func (v *SessionView) Do(steps ...Step) error {
	ctx, cleanup, err := v.operationContext()
	if err != nil {
		return err
	}
	defer cleanup()
	s := v.session
	if err := s.preflightSteps(ctx, steps); err != nil {
		return errors.Join(s.ensureOpen(), err)
	}
	// The execution loop checks authority again before each dispatch. Its
	// session lifetime check is the same one used by the strict input bundle.
	return s.runSteps(ctx, steps)
}

func (s *sessionCore) preflightSteps(ctx context.Context, steps []Step) error {
	for i, step := range steps {
		if err := ctx.Err(); err != nil {
			return &StepError{Index: i, Operation: step.kind, Err: err}
		}
		validationErr := step.validate(ctx)
		if err := ctx.Err(); err != nil {
			return &StepError{Index: i, Operation: step.kind, Err: err}
		}
		if validationErr != nil {
			return &StepError{Index: i, Operation: step.kind, Err: validationErr}
		}
		op := "keyboard"
		if step.kind == "delay" {
			continue
		}
		if step.kind == "click" {
			op = "click"
		}
		if s.input == nil {
			return &StepError{Index: i, Operation: step.kind, Err: &CapabilityError{Capability: CapabilityInput, Operation: op, Err: ErrUnavailable}}
		}
		if err := s.input.CheckAvailable(op); err != nil {
			return &StepError{Index: i, Operation: step.kind, Err: err}
		}
	}
	return ctx.Err()
}

func (s *sessionCore) runSteps(ctx context.Context, steps []Step) error {
	return s.runStepsChecked(ctx, steps, nil)
}

// A guard observes the current target immediately before each input mutation.
// A failed guard is never an input dispatch and must not trigger a retry.
func (s *sessionCore) runStepsChecked(ctx context.Context, steps []Step, guard func(context.Context) error) error {
	for i, step := range steps {
		if err := s.ensureOpen(); err != nil {
			return &StepError{Index: i, Operation: step.kind, Err: err}
		}
		if err := ctx.Err(); err != nil {
			return &StepError{Index: i, Operation: step.kind, Err: err}
		}
		if guard != nil && step.kind != "delay" {
			if err := guard(ctx); err != nil {
				return &StepError{Index: i, Operation: step.kind, Err: err}
			}
		}
		err := s.runStep(ctx, step)
		if closed := s.ensureOpen(); closed != nil {
			err = errors.Join(closed, err)
		} else if ctx.Err() != nil {
			err = errors.Join(ctx.Err(), err)
		}
		if err != nil {
			return &StepError{Index: i, Operation: step.kind, Err: err, MayHaveDispatched: step.kind != "delay"}
		}
	}
	return nil
}

func (s *sessionCore) runStep(ctx context.Context, step Step) error {
	var err error
	switch step.kind {
	case "type-literal":
		err = s.input.TypeLiteral(ctx, step.text)
	case "type-keys":
		err = s.input.Type(ctx, step.text)
	case "key", "hotkey":
		err = s.input.Type(ctx, "{"+step.text+"}")
	case "click":
		err = s.input.MouseClick(ctx, step.x, step.y, 1)
	case "delay":
		timer := time.NewTimer(step.delay)
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-timer.C:
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	return err
}

// ErrWindowFocusUnverified means the requested window could not be observed
// as the active window. No input is sent after this error is detected.
var ErrWindowFocusUnverified = errors.New("perfuncted: window focus cannot be verified")

// ErrWindowIdentityChanged means fresh window metadata no longer matches the
// instance selected for the batch. A batch never re-resolves a different title
// match after selection.
var ErrWindowIdentityChanged = errors.New("perfuncted: selected window identity changed")

// WindowTarget is a lazy, non-owning, exact-title window selector.
// It does not activate anything until Do is invoked.
type WindowTarget struct {
	view  *SessionView
	title string
}

// Window selects a window by exact, case-insensitive title for a targeted batch.
// Pass Require(CapabilityWindows) to OpenDefault to enable this operation.
func (s *sessionCore) Window(title string) *WindowTarget {
	return (&SessionView{session: s, ctx: context.Background()}).Window(title)
}

// Window retains the view's cancellation and timeout configuration.
func (v *SessionView) Window(title string) *WindowTarget {
	return &WindowTarget{view: v, title: title}
}

func windowTargetIdentity(current, selected window.Info) error {
	if current.StableID() != selected.StableID() ||
		current.PID != selected.PID ||
		current.AppID != selected.AppID ||
		current.Class != selected.Class {
		return ErrWindowIdentityChanged
	}
	if !current.Active {
		return ErrWindowFocusUnverified
	}
	return nil
}

// Do resolves a unique managed window, activates it once, waits for observed
// focus, and checks fresh target metadata before each input step. Focus and
// identity observations are point-in-time checks, not atomic input ownership.
// A compositor that cannot report Active or identity fails closed. Native ID
// recycling with identical published metadata cannot be ruled out; clients
// needing stronger guarantees should use the scoped accessibility locator.
//
//nolint:gocyclo // All identity, focus, lifecycle and dispatch gates fail closed independently.
func (t *WindowTarget) Do(steps ...Step) error {
	if t == nil || t.view == nil || t.view.session == nil {
		return ErrNilSession
	}
	ctx, cleanup, err := t.view.operationContext()
	if err != nil {
		return err
	}
	defer cleanup()
	s := t.view.session
	if strings.TrimSpace(t.title) == "" {
		return fmt.Errorf("window target: empty title: %w", ErrInvalidArgument)
	}
	// Preflight every input step before discovering or activating the window.
	if preflightErr := s.preflightSteps(ctx, steps); preflightErr != nil {
		return preflightErr
	}
	for i, step := range steps {
		if step.kind == "click" {
			return &StepError{Index: i, Operation: step.kind,
				Err: fmt.Errorf("window-targeted clicks have global coordinates, not a proven window-relative target: %w", ErrUnsupported)}
		}
	}
	if len(steps) == 0 {
		return nil
	}
	if availabilityErr := s.windows.checkAvailable("discover"); availabilityErr != nil {
		return availabilityErr
	}
	w, err := s.windows.Find(ctx, WindowMatch{TitleExact: t.title})
	if err != nil {
		return err
	}
	selected, err := w.Info(ctx)
	if err != nil {
		return err
	}
	// An ID and title alone cannot distinguish a recycled compositor handle.
	if selected.PID == 0 && selected.AppID == "" && selected.Class == "" {
		return fmt.Errorf("window target has no independent application identity: %w", ErrUnsupported)
	}
	if activateErr := w.Activate(ctx); activateErr != nil {
		return activateErr
	}
	guard := func(ctx context.Context) error {
		info, infoErr := w.Info(ctx)
		if infoErr != nil {
			return infoErr
		}
		return windowTargetIdentity(info, selected)
	}

	// Activation is asynchronous on several compositors. Wait for an observed
	// focus change; never interpret a successful activation request as focus.
	focusCtx, cancelFocus := context.WithTimeout(ctx, s.Timeouts().Short)
	defer cancelFocus()
	err = s.Wait(focusCtx, Predicate("window target active", func(ctx context.Context) (bool, error) {
		info, infoErr := w.Info(ctx)
		if infoErr != nil {
			return false, infoErr
		}
		if info.StableID() != selected.StableID() ||
			info.PID != selected.PID || info.AppID != selected.AppID ||
			info.Class != selected.Class {
			return false, ErrWindowIdentityChanged
		}
		return info.Active, nil
	}))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return fmt.Errorf("window target activation not observed: %w: %w", ErrWindowFocusUnverified, err)
		}
		return err
	}
	// runStepsChecked checks session authority before each operation, including
	// after an activation or observation that completes during session shutdown.
	return s.runStepsChecked(ctx, steps, guard)
}

// OpenDefault opens a session with the default convenience capabilities.
func OpenDefault(opts ...Option) (*Session, error) {
	s, err := openDefault(opts...)
	return wrapSession(s), err
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
