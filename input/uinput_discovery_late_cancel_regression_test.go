//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
)

type cancelingUinputLister struct {
	cancel context.CancelFunc
	closed bool
}

func (l *cancelingUinputLister) List(context.Context) ([]output.Info, error) {
	l.cancel()
	return []output.Info{{Available: true, Geometry: output.Geometry{W: 1920, H: 1080}}}, nil
}
func (l *cancelingUinputLister) Close() error { l.closed = true; return nil }

// Enumeration is not authorization to create a kernel input device after the
// original caller has revoked its operation, even when metadata is valid.
func TestUinputDiscoveryCancellationDoesNotCreateDevice(t *testing.T) {
	oldOpen, oldCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister, newUinputBackend = oldOpen, oldCreate })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lister := &cancelingUinputLister{cancel: cancel}
	openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) { return lister, nil }
	created := 0
	newUinputBackend = func(int32, int32) (Inputter, error) {
		created++
		return noopInputter{}, nil
	}
	backend, err := newResolvedUinputBackend(ctx, env.Runtime{}, 0, 0)
	if !errors.Is(err, context.Canceled) || backend != nil || created != 0 || !lister.closed {
		t.Fatalf("revoked discovery: backend=%T err=%v created=%d closed=%v", backend, err, created, lister.closed)
	}
}

func TestUinputExplicitDimensionsCanceledBeforeDeviceCreation(t *testing.T) {
	oldCreate := newUinputBackend
	t.Cleanup(func() { newUinputBackend = oldCreate })
	created := 0
	newUinputBackend = func(int32, int32) (Inputter, error) { created++; return noopInputter{}, nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend, err := newResolvedUinputBackend(ctx, env.Runtime{}, 1920, 1080)
	if !errors.Is(err, context.Canceled) || backend != nil || created != 0 {
		t.Fatalf("revoked explicit dimensions: backend=%T err=%v created=%d", backend, err, created)
	}
}

// The kernel device may have been opened by the time cancellation arrives.
// It must be closed rather than handed to a caller whose operation has ended.
func TestUinputCancellationDuringConstructorClosesCreatedDevice(t *testing.T) {
	oldCreate := newUinputBackend
	t.Cleanup(func() { newUinputBackend = oldCreate })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	created := &selectorTrackedInputter{}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		cancel()
		return created, nil
	}
	backend, err := newResolvedUinputBackend(ctx, env.Runtime{}, 1920, 1080)
	if !errors.Is(err, context.Canceled) || backend != nil || created.closed != 1 {
		t.Fatalf("constructor canceled: backend=%T err=%v close count=%d", backend, err, created.closed)
	}
}
