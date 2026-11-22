package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nskaggs/perfuncted/window"
)

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
func (s *Session) Window(title string) *WindowTarget {
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
	if availabilityErr := s.Windows.checkAvailable("discover"); availabilityErr != nil {
		return availabilityErr
	}
	w, err := s.Windows.Find(ctx, WindowMatch{TitleExact: t.title})
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
