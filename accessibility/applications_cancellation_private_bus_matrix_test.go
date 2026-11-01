package accessibility

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
)

type cancelAtAccessibilityContextRead struct {
	context.Context
	cancel    context.CancelFunc
	threshold int32
	calls     atomic.Int32
}

func (c *cancelAtAccessibilityContextRead) Err() error {
	if c.calls.Add(1) == c.threshold {
		c.cancel()
	}
	return c.Context.Err()
}

// Run cancellation at each observed admission and provider boundary rather
// than only before the first call. No private app data may escape a canceled
// AT-SPI enumeration, even if optional provider properties fail after cancel.
func TestATSPISyntheticApplicationsCancellationBoundaryMatrix(t *testing.T) {
	b, _, _ := newSyntheticATSPIBackend(t, false)
	normalBase, normalCancel := context.WithCancel(context.Background())
	baseline := &cancelAtAccessibilityContextRead{Context: normalBase, cancel: normalCancel, threshold: 10000}
	apps, err := b.Applications(baseline)
	normalCancel()
	if err != nil || len(apps) != 1 || apps[0].Name != "Editor" {
		t.Fatalf("real AT-SPI application baseline=%+v err=%v", apps, err)
	}
	checks := baseline.calls.Load()
	t.Logf("AT-SPI application enumeration cancellation checks=%d", checks)
	if checks < 2 {
		t.Fatalf("application enumeration lacks cancellation checks: %d", checks)
	}
	for index := int32(1); index <= checks; index++ {
		t.Run("cancellation-read-"+stringInt32(index), func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelAtAccessibilityContextRead{Context: base, cancel: cancel, threshold: index}
			result, err := b.Applications(ctx)
			if base.Err() == nil {
				t.Skipf("cached path used fewer checks: %d", ctx.calls.Load())
			}
			if len(result) != 0 || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled AT-SPI enumeration returned private applications=%+v err=%v (cancel after Err read %d, total %d)", result, err, index, ctx.calls.Load())
			}
		})
	}
}
func stringInt32(n int32) string { return strconv.FormatInt(int64(n), 10) }

func TestATSPISyntheticFreshApplicationCancellationBoundaryMatrix(t *testing.T) {
	b, _, _ := newSyntheticATSPIBackend(t, false)
	filter := ApplicationFilter{Name: "Editor"}
	normalBase, normalCancel := context.WithCancel(context.Background())
	baseline := &cancelAtAccessibilityContextRead{Context: normalBase, cancel: normalCancel, threshold: 10000}
	app, err := b.FindApplicationFresh(baseline, filter)
	normalCancel()
	if err != nil || app.Name != "Editor" {
		t.Fatalf("fresh private bus app baseline=%+v err=%v", app, err)
	}
	checks := baseline.calls.Load()
	t.Logf("AT-SPI fresh application checks=%d", checks)
	for index := int32(1); index <= checks; index++ {
		t.Run("fresh-cancel-"+stringInt32(index), func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelAtAccessibilityContextRead{Context: base, cancel: cancel, threshold: index}
			result, err := b.FindApplicationFresh(ctx, filter)
			if base.Err() == nil {
				t.Skipf("lookup read fewer contexts (%d)", ctx.calls.Load())
			}
			if !errors.Is(err, context.Canceled) || result.Name != "" {
				t.Fatalf("fresh application lookup returned private identity=%q on cancellation: %v (cancel at Err %d, total %d)", result.Name, err, index, ctx.calls.Load())
			}
		})
	}
}
