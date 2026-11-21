package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// CLI configuration must construct valid nested and trace options while
// respecting an already-canceled request, without starting a host session.
// In particular trace-delay alone must enable tracing, not just tracing with
// the explicit --trace-actions switch.
func TestCLIConfiguredNestedAndTraceOptionsRespectCanceledOpen(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  cliConfig
	}{
		{name: "nested", cfg: cliConfig{nested: true}},
		{name: "trace actions", cfg: cliConfig{traceActions: true}},
		{name: "trace delay implies trace", cfg: cliConfig{traceDelay: 10 * time.Millisecond}},
		{name: "nested trace delay", cfg: cliConfig{nested: true, traceActions: true, traceDelay: time.Millisecond}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			session, err := defaultOpenPFFactory(&tc.cfg)(ctx)
			if session != nil {
				_ = session.Close()
				t.Fatal("canceled CLI option open returned live session")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled CLI option open returned %v, want context cancellation", err)
			}
		})
	}
}
