package window

import (
	"context"
	"errors"
	"testing"
)

type cancelSwayContextAfterErrCheck struct {
	context.Context
	cancel      context.CancelFunc
	cancelAfter int
	checks      int
}

func (c *cancelSwayContextAfterErrCheck) Err() error {
	c.checks++
	err := c.Context.Err()
	if c.checks == c.cancelAfter {
		c.cancel()
	}
	return err
}

func TestSwayIteratorWithholdsTreeWhenCanceledAfterSuccessfulQuery(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"private-window"}]}`
	for _, tc := range []struct {
		name        string
		cancelAfter int
	}{
		{name: "after query response", cancelAfter: 6},
		{name: "before tree traversal", cancelAfter: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelSwayContextAfterErrCheck{Context: base, cancel: cancel, cancelAfter: tc.cancelAfter}
			manager := &SwayManager{conn: newSuccessSwayConn([]byte(tree))}

			var windows []Info
			var resultErr error
			for info, err := range manager.IterateWindows(ctx) {
				if err != nil {
					resultErr = err
					break
				}
				windows = append(windows, info)
			}
			if !errors.Is(resultErr, context.Canceled) || len(windows) != 0 {
				t.Fatalf("canceled tree query returned windows=%+v err=%v", windows, resultErr)
			}
			if ctx.checks != tc.cancelAfter+1 {
				t.Fatalf("cancellation check count=%d, want %d", ctx.checks, tc.cancelAfter+1)
			}
		})
	}
}

func TestSwayActiveTitleWithholdsTreeWhenCanceledAfterSuccessfulQuery(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelSwayContextAfterErrCheck{Context: base, cancel: cancel, cancelAfter: 6}
	tree := `{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"private-title","focused":true}]}`
	manager := &SwayManager{conn: newSuccessSwayConn([]byte(tree))}

	title, err := manager.ActiveTitle(ctx)
	if title != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled active-title query returned title=%q err=%v", title, err)
	}
	if ctx.checks != 7 {
		t.Fatalf("cancellation check count=%d, want 7", ctx.checks)
	}
}
