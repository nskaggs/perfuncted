package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Readiness is not authorization to continue a canceled startup: cancellation
// may arrive while a filesystem or compositor predicate is executing.
func TestPollConditionRejectsCancellationDuringSuccessfulProbe(t *testing.T) {
	for _, reportsReady := range []bool{true, false} {
		t.Run(map[bool]string{true: "ready", false: "not-ready"}[reportsReady], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probes := 0
			err := pollCondition(ctx, 1, time.Millisecond, func() bool {
				probes++
				cancel()
				return reportsReady
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled readiness probe returned %v; want cancellation", err)
			}
			if probes != 1 {
				t.Fatalf("unexpected readiness probes: %d", probes)
			}
		})
	}
}
