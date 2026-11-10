package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A compositor opener may ignore its context and finish creating a backend
// after the caller has canceled startup. The session must not install it.
func TestCapabilityOpenRejectsBackendReturnedAfterCancellation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		action       func(context.CancelFunc)
		cancelBefore bool
	}{
		{"canceled before opening", func(context.CancelFunc) {}, true},
		{"canceled while opening", func(cancel context.CancelFunc) { cancel() }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelBefore {
				cancel()
			}
			backend := &trackedCapabilityBackend{}
			opened, configured, installed := 0, 0, 0
			got, err := openCapabilityBackend(ctx, time.Second, CapabilityScreen,
				func(context.Context) (*trackedCapabilityBackend, error) {
					opened++
					tc.action(cancel)
					return backend, nil
				},
				func(*trackedCapabilityBackend) { configured++ },
				func(*trackedCapabilityBackend) { installed++ },
			)
			if !errors.Is(err, context.Canceled) || got != nil {
				t.Fatalf("late backend returned handle=%v err=%v, want nil and canceled", got, err)
			}
			if configured != 0 || installed != 0 {
				t.Fatalf("canceled backend configured=%d installed=%d", configured, installed)
			}
			if opened != 0 && backend.closes != 1 {
				t.Fatalf("late backend closed %d times, want exactly once", backend.closes)
			}
			if tc.cancelBefore && opened != 0 {
				t.Fatalf("pre-canceled startup invoked opener %d times", opened)
			}
		})
	}
}

// Configuration may itself trigger cancellation; installation is still
// forbidden when authority expires between configure and install.
func TestCapabilityOpenRejectsCancellationDuringConfiguration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &trackedCapabilityBackend{}
	installed := 0
	got, err := openCapabilityBackend(ctx, time.Second, CapabilityOutputs,
		func(context.Context) (*trackedCapabilityBackend, error) { return backend, nil },
		func(*trackedCapabilityBackend) { cancel() },
		func(*trackedCapabilityBackend) { installed++ },
	)
	if !errors.Is(err, context.Canceled) || got != nil || installed != 0 || backend.closes != 1 {
		t.Fatalf("canceled configuration handle=%v err=%v installed=%d closes=%d", got, err, installed, backend.closes)
	}
}

// A backend that returns only after the startup deadline has expired must be
// discarded even if its own opener reports success instead of deadline error.
func TestCapabilityOpenRejectsLateSuccessAfterStartupDeadline(t *testing.T) {
	backend := &trackedCapabilityBackend{}
	installed := 0
	got, err := openCapabilityBackend(context.Background(), 3*time.Millisecond, CapabilityClipboard,
		func(ctx context.Context) (*trackedCapabilityBackend, error) {
			<-ctx.Done()
			return backend, nil
		}, nil,
		func(*trackedCapabilityBackend) { installed++ },
	)
	if !errors.Is(err, context.DeadlineExceeded) || got != nil || installed != 0 || backend.closes != 1 {
		t.Fatalf("expired setup handle=%v err=%v installed=%d closes=%d", got, err, installed, backend.closes)
	}
}
