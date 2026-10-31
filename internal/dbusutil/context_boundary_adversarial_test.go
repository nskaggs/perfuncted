//go:build linux

package dbusutil

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDBusPublicContextBoundaries(t *testing.T) {
	if c, err := SessionBusAddressContext(absentDBusContext, "unix:path=/nonexistent"); c != nil || err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("session nil = %v %v", c, err)
	}
	if c, err := ConnectContext(absentDBusContext, "unix:path=/nonexistent"); c != nil || err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("connect nil = %v %v", c, err)
	}
	if present, err := HasServiceContext(absentDBusContext, nil, "service"); present || err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("service nil = %v %v", present, err)
	}
	if present, err := HasServiceContext(context.Background(), nil, "service"); present || err != nil {
		t.Fatalf("service no connection = %v %v", present, err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if c, err := SessionBusAddressContext(ctx, ""); c != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("session expired = %v %v", c, err)
	}
	if c, err := ConnectContext(ctx, "unix:path=/nonexistent"); c != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("connect expired = %v %v", c, err)
	}
	if present, err := HasServiceContext(ctx, nil, "service"); present || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("service expired = %v %v", present, err)
	}
}

func TestDBusHandshakeFailureAndCancellationCleanup(t *testing.T) {
	sentinel := errors.New("authentication refused")
	if err := runHandshakeContext(context.Background(), func() error { return sentinel }, func() error { t.Error("closed successful handshake"); return nil }); !errors.Is(err, sentinel) {
		t.Fatalf("handshake error = %v", err)
	}
	if err := runHandshakeContext(absentDBusContext, func() error { t.Error("executed with nil context"); return nil }, func() error { return nil }); err == nil {
		t.Fatal("nil context accepted")
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var closed atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runHandshakeContext(ctx, func() error { close(started); <-release; return sentinel }, func() error { closed.Add(1); close(release); return nil })
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled handshake = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handshake did not stop")
	}
	if closed.Load() != 1 {
		t.Fatalf("transport closed %d times", closed.Load())
	}
}

// An intentionally absent context exercises the public nil-context boundary.
var absentDBusContext context.Context
