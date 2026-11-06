package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A submitted operation may be canceled after the writer has admitted it.
// The writer must retire its deadline callback, preserve both cancellation
// and a real device failure, and continue accepting later authorized work.
func TestRemoteDesktopWriterCancellationRetiresDeadlineForNextOperation(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	released := make(chan struct{})
	errorFromDevice := errors.New("synthetic device write failure")
	first := make(chan error, 1)
	go func() {
		first <- b.submit(ctx, func() error {
			close(started)
			<-released
			return errorFromDevice
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("writer did not admit first request")
	}
	cancel()
	close(released)
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled request accepted: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled request never returned")
	}
	// The first caller can return before its writer finishes draining. A second
	// command is serialized behind the first and must not inherit its deadline.
	nextCtx, nextCancel := context.WithTimeout(context.Background(), time.Second)
	defer nextCancel()
	ran := false
	if err := b.submit(nextCtx, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("writer failed to recover from canceled operation: executed=%v err=%v", ran, err)
	}
	if b.stateError() != nil {
		t.Fatalf("cancellation revoked portal authority: %v", b.stateError())
	}
}

// Inspect the writer receipt directly: submit can return on ctx.Done before
// observing command.done, but the writer must retain the underlying device
// failure when it records a terminal canceled operation.
func TestRemoteDesktopWriterReceiptPreservesDeviceFailureAndCancellation(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	deviceFailure := errors.New("synthetic keyboard failure")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	command := remoteDesktopCommand{ctx: ctx, done: make(chan error, 1), run: func() error {
		close(started)
		<-release
		return deviceFailure
	}}
	select {
	case b.commands <- command:
	case <-time.After(time.Second):
		t.Fatal("command not admitted")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("writer did not run command")
	}
	cancel()
	close(release)
	select {
	case err := <-command.done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, deviceFailure) {
			t.Fatalf("writer receipt discarded error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer never acknowledged canceled operation")
	}
	nextCtx, nextCancel := context.WithTimeout(context.Background(), time.Second)
	defer nextCancel()
	if err := b.submit(nextCtx, func() error { return nil }); err != nil {
		t.Fatalf("failed operation poisoned writer: %v", err)
	}
}
