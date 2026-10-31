//go:build linux

package dbusutil

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestHandshakeCannotAcknowledgeSuccessAfterCallerCanceledWithinReply(t *testing.T) {
	for i := 0; i < 120; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		var closes atomic.Int32
		err := runHandshakeContext(ctx, func() error { cancel(); return nil }, func() error { closes.Add(1); return nil })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d acknowledged successful D-Bus authentication after canceled caller: %v", i, err)
		}
		cancel()
	}
}
func TestHandshakeWithPreCanceledCallerDoesNotAttemptAuthentication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var handshakes atomic.Int32
	err := runHandshakeContext(ctx, func() error { handshakes.Add(1); return nil }, func() error { return nil })
	if !errors.Is(err, context.Canceled) || handshakes.Load() != 0 {
		t.Fatalf("pre-canceled handshake ran %d times, result %v", handshakes.Load(), err)
	}
}
