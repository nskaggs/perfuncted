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

// Absent optional interfaces are represented by object ID zero. Object zero
// is a global EIS protocol object and must never be confused with a device
// teardown merely because an optional device was not offered by the server.
func TestRemoteDesktopUnboundOptionalDeviceDoesNotRevokeOnGlobalObjectZero(t *testing.T) {
	backend, _, client := newSyntheticRevocableRemoteDesktop(t)
	backend.sender.scroll, backend.sender.scrollDev = 0, 0
	backend.sender.text, backend.sender.textDev = 0, 0
	if backend.isEISLifecycleFailure(eiMessage{object: 0, opcode: 0}) {
		t.Fatal("global EIS object zero misidentified as unbound device removal")
	}
	client.messages <- eiMessage{object: 0, opcode: 0}
	select {
	case <-backend.revoked:
		t.Fatal("unrelated global EIS event revoked live portal authorization")
	case <-time.After(40 * time.Millisecond):
	}
	if err := backend.stateError(); err != nil {
		t.Fatalf("global event invalidated authorized session: %v", err)
	}
	client.messages <- eiMessage{object: backend.sender.keyboardDev, opcode: 8}
	select {
	case <-backend.revoked:
	case <-time.After(time.Second):
		t.Fatal("real selected device suspension did not revoke access")
	}
}

func TestRemoteDesktopZeroDistanceScrollDoesNotAcknowledgeCanceledOrNilOperation(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "nil operation context", ctx: nil, want: ErrRemoteDesktopUnavailable},
		{name: "pre-canceled operation", ctx: func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }(), want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newSyntheticRevocableRemoteDesktop(t)
			for _, run := range []func(context.Context, int) error{b.ScrollUp, b.ScrollDown, b.ScrollLeft, b.ScrollRight} {
				err := run(tc.ctx, 0)
				if tc.ctx == nil {
					if err == nil {
						t.Fatal("nil context zero-scroll reported success")
					}
				} else if !errors.Is(err, tc.want) {
					t.Fatalf("canceled zero scroll reported success: %v", err)
				}
			}
			if len(b.heldKeys) != 0 || len(b.buttons) != 0 {
				t.Fatalf("zero scroll changed input state: keys=%v buttons=%v", b.heldKeys, b.buttons)
			}
		})
	}
}
