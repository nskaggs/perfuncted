package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/window"
	"time"
)

var errFocusNotVerified = errors.New("window activation not verified")

// waitWindowActive reports success only after the authoritative window manager
// confirms the chosen stable ID is active, not when an event is merely queued.
func waitWindowActive(ctx context.Context, w *perfuncted.Window, timeout, poll time.Duration) error {
	if timeout <= 0 || poll <= 0 {
		return fmt.Errorf("verification durations must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		info, err := w.Info(ctx)
		if err == nil && info.Active {
			return nil
		}
		if err != nil {
			return fmt.Errorf("verify activation of window %s: %w", w.ID().String(), err)
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("window %s never reported active: %w", w.ID().String(), errFocusNotVerified)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// waitWindowGone confirms a dialog or window disappeared, which is stronger
// than observing that a close or Enter keystroke was merely sent. It also
// prevents restoring clipboard contents before an asynchronous paste is read.
func waitWindowGone(ctx context.Context, bundle *perfuncted.WindowBundle, id string, timeout, poll time.Duration) error {
	if id == "" || timeout <= 0 || poll <= 0 {
		return fmt.Errorf("window disappearance verification needs an ID and positive durations")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		wins, err := bundle.List(ctx, window.Match{})
		if err != nil {
			return fmt.Errorf("verify window %s disappeared: %w", id, err)
		}
		found := false
		for _, w := range wins {
			if w.ID().String() == id {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("window %s remains open: %w", id, errWindowNotGone)
			}
			return ctx.Err()
		case <-tick.C:
		}
	}
}

var errWindowNotGone = errors.New("window disappearance not verified")
