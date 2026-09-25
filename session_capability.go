package perfuncted

import (
	"context"
	"fmt"
	"time"

	"github.com/nskaggs/perfuncted/internal/util"
)

//nolint:contextcheck // capability setup derives the effective startup policy.
func openCapabilityBackend[T any](
	parent context.Context,
	timeout time.Duration,
	capability Capability,
	open func(context.Context) (T, error),
	configure func(T),
	install func(T),
) (T, error) {
	// Capability setup inherits caller cancellation and also has a bounded
	// startup policy so direct-call backend fallbacks cannot extend Open forever.
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	backend, err := open(ctx)
	backend, err = validateBackend(capability, backend, err)
	if err == nil {
		if configure != nil {
			configure(backend)
		}
		install(backend)
	}
	closeFailedBackend(backend, err)
	return backend, err
}

func configureScreenBackendTimeout(backend any, timeout time.Duration) {
	if backend == nil {
		return
	}
	if configurable, ok := backend.(interface{ SetCaptureTimeout(time.Duration) }); ok {
		configurable.SetCaptureTimeout(timeout)
	}
}

func validateBackend[T any](capability Capability, backend T, err error) (T, error) {
	if err == nil && util.IsNil(backend) {
		err = nilBackendError(capability)
	}
	return backend, err
}

func nilBackendError(capability Capability) error {
	return fmt.Errorf("perfuncted: %s backend returned nil: %w", capability, ErrUnavailable)
}

func closeFailedBackend(backend any, openErr error) {
	if openErr == nil || util.IsNil(backend) {
		return
	}
	if closer, ok := backend.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}
