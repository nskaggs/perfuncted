package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/keymap"
)

// OpenDefault opens a session requiring input and no other capabilities by
// default. Explicit optional input remains optional. Startup uses the selected
// target's startup timeout. Wayland authorization may be requested at startup.
func OpenDefault(opts ...Option) (*Session, error) {
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
	return Open(ctx, func(dst *openConfig) error { *dst = cfg; return nil })
}

// SessionView holds immutable operation settings for a Session. It does not
// own the session; closing the Session cancels operations through the view.
type SessionView struct {
	session *Session
	ctx     context.Context //nolint:containedctx // The view explicitly carries caller context.
	timeout time.Duration
}

// WithContext returns a non-owning view. A nil context is rejected on use.
func (s *Session) WithContext(ctx context.Context) *SessionView {
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
func (s *Session) WithTimeout(d time.Duration) *SessionView {
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
func (s *Session) Do(steps ...Step) error {
	return (&SessionView{session: s, ctx: context.Background()}).Do(steps...)
}

// Type immediately sends literal text to the currently focused application.
func (s *Session) Type(text string) error { return s.Do(Type(text)) }

// Key immediately sends one named key to the currently focused application.
func (s *Session) Key(name string) error { return s.Do(Key(name)) }

// Type immediately sends literal text using the view's context and timeout.
func (v *SessionView) Type(text string) error { return v.Do(Type(text)) }

// Key immediately sends one named key using the view's context and timeout.
func (v *SessionView) Key(name string) error { return v.Do(Key(name)) }

// Do executes steps under the view's context, timeout and session lifetime.
func (v *SessionView) Do(steps ...Step) error {
	if v == nil || v.session == nil {
		return ErrNilSession
	}
	s := v.session
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if v.ctx == nil || v.timeout < 0 {
		return ErrInvalidArgument
	}
	if err := v.ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(v.ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	if v.timeout > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, v.timeout)
		defer timeoutCancel()
	}
	if err := s.preflightSteps(ctx, steps); err != nil {
		if closed := s.ensureOpen(); closed != nil {
			return errors.Join(closed, err)
		}
		return err
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	return s.runSteps(ctx, steps)
}

func (s *Session) preflightSteps(ctx context.Context, steps []Step) error {
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
		if s.Input == nil {
			return &StepError{Index: i, Operation: step.kind, Err: &CapabilityError{Capability: CapabilityInput, Operation: op, Err: ErrUnavailable}}
		}
		if err := s.Input.checkAvailable(op); err != nil {
			return &StepError{Index: i, Operation: step.kind, Err: err}
		}
	}
	return ctx.Err()
}

func (s *Session) runSteps(ctx context.Context, steps []Step) error {
	for i, step := range steps {
		if err := s.ensureOpen(); err != nil {
			return &StepError{Index: i, Operation: step.kind, Err: err}
		}
		if err := ctx.Err(); err != nil {
			return &StepError{Index: i, Operation: step.kind, Err: err}
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

func (s *Session) runStep(ctx context.Context, step Step) error {
	var err error
	switch step.kind {
	case "type-literal":
		err = s.Input.TypeLiteral(ctx, step.text)
	case "type-keys":
		err = s.Input.Type(ctx, step.text)
	case "key", "hotkey":
		err = s.Input.Type(ctx, "{"+step.text+"}")
	case "click":
		err = s.Input.MouseClick(ctx, step.x, step.y, 1)
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
