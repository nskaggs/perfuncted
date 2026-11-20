//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// Simulate cancellation immediately after a valid explicit logical geometry
// is checked, without relying on scheduler timing or a real kernel device.
type cancelAtSecondUinputCheck struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelAtSecondUinputCheck) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestUinputCancellationAfterGeometryValidationDoesNotOpenDevice(t *testing.T) {
	original := newUinputBackend
	t.Cleanup(func() { newUinputBackend = original })
	created := 0
	newUinputBackend = func(int32, int32) (Inputter, error) {
		created++
		return noopInputter{}, nil
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAtSecondUinputCheck{Context: base, cancel: cancel}
	backend, err := newResolvedUinputBackend(ctx, env.Runtime{}, 1920, 1080)
	if backend != nil || !errors.Is(err, context.Canceled) || created != 0 || ctx.checks != 2 {
		t.Fatalf("post-validation cancellation admitted kernel device: backend=%T err=%v created=%d checks=%d", backend, err, created, ctx.checks)
	}
}
