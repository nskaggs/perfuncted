//go:build linux

package input

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
)

func TestUinputLogicalBoundsIgnoreUnavailableOutputsAndRejectOverflow(t *testing.T) {
	width, height, err := uinputBoundsFromOutputs([]output.Info{
		{Geometry: output.Geometry{X: -500, Y: -300, W: 9000, H: 9000}},
		{Available: true, Geometry: output.Geometry{X: 10, Y: 20, W: 1920, H: 1080}},
	})
	if err != nil || width != 1930 || height != 1100 {
		t.Fatalf("logical bounds = %dx%d, %v; want 1930x1100", width, height, err)
	}

	_, _, err = uinputBoundsFromOutputs([]output.Info{{
		Available: true,
		Geometry:  output.Geometry{X: math.MaxInt32 - 10, W: 20, H: 1080},
	}})
	if err == nil || !strings.Contains(err.Error(), "exceeds input device range") {
		t.Fatalf("overflowing output geometry accepted: %v", err)
	}
}

func TestUinputLogicalBoundsDiscoveryPreservesCancellationAndOpenFailure(t *testing.T) {
	oldOpen := openUinputOutputLister
	t.Cleanup(func() { openUinputOutputLister = oldOpen })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opened := false
	openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) {
		opened = true
		return nil, nil
	}
	if _, _, _, err := discoverUinputLogicalBounds(ctx, env.Runtime{}); !errors.Is(err, context.Canceled) || opened {
		t.Fatalf("pre-canceled discovery opened lister=%t err=%v", opened, err)
	}

	openFailure := errors.New("synthetic output discovery failure")
	openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) {
		return nil, openFailure
	}
	oldCreate := newUinputBackend
	newUinputBackend = func(int32, int32) (Inputter, error) {
		t.Fatal("output discovery failure reached device creation")
		return nil, nil
	}
	t.Cleanup(func() { newUinputBackend = oldCreate })
	backend, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 0, 0)
	if backend != nil || !errors.Is(err, openFailure) {
		t.Fatalf("failed output discovery published backend=%T err=%v", backend, err)
	}
}

func TestUinputResolvedBackendRejectsInvalidDimensionsBeforeDeviceCreation(t *testing.T) {
	oldCreate := newUinputBackend
	t.Cleanup(func() { newUinputBackend = oldCreate })
	newUinputBackend = func(int32, int32) (Inputter, error) {
		t.Fatal("invalid touchpad dimensions reached device creation")
		return nil, nil
	}
	backend, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 1, 1080)
	if backend != nil || err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("invalid dimensions returned backend=%T err=%v", backend, err)
	}
}
