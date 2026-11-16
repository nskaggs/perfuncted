//go:build linux

package input

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// The first four Err checks cover API preflight, submission, writer admission,
// and the first action in a two-action Type batch.
const portalTypeSecondActionErrCall = 5

type portalTypeActionContextGate struct {
	context.Context
	calls   atomic.Int32
	reached chan struct{}
	release chan struct{}
}

func (c *portalTypeActionContextGate) Err() error {
	if c.calls.Add(1) == portalTypeSecondActionErrCall {
		close(c.reached)
		select {
		case <-c.release:
		case <-c.Context.Done():
		}
	}
	return c.Context.Err()
}

func TestRemoteDesktopTypeStopsBeforeNextActionAfterCancellation(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	baseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx := &portalTypeActionContextGate{
		Context: baseCtx,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() { result <- backend.Type(ctx, "a{enter}") }()
	select {
	case <-ctx.reached:
	case <-baseCtx.Done():
		t.Fatalf("Type did not reach the next action boundary: %v", baseCtx.Err())
	}
	if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	textFrame, err := readSyntheticEISFrame(peer)
	if err != nil {
		t.Fatalf("read first text frame: %v", err)
	}
	if textFrame.object != backend.sender.text || textFrame.opcode != 2 {
		t.Fatalf("first action frame = object %d opcode %d, want text object %d opcode 2", textFrame.object, textFrame.opcode, backend.sender.text)
	}
	text, err := eiReadString(textFrame.body, 0)
	if err != nil || text != "a" {
		t.Fatalf("first action text = %q, %v; want %q", text, err, "a")
	}
	deviceFrame, err := readSyntheticEISFrame(peer)
	if err != nil {
		t.Fatalf("read first text device frame: %v", err)
	}
	if deviceFrame.object != backend.sender.textDev || deviceFrame.opcode != 3 || len(deviceFrame.body) != 12 {
		t.Fatalf("first text device frame = %+v", deviceFrame)
	}
	if err := peer.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	cancel()
	close(ctx.release)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Type returned %v after cancellation, want context.Canceled", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close writer after canceled batch: %v", err)
	}
	stopFrame, err := readSyntheticEISFrame(peer)
	if err != nil {
		t.Fatalf("read EIS device stop frame: %v", err)
	}
	if stopFrame.object != backend.sender.keyboardDev || stopFrame.opcode != 2 || len(stopFrame.body) != 4 {
		t.Fatalf("frame after canceled Type batch = %+v, want device stop request", stopFrame)
	}
	if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var unexpected [1]byte
	if n, err := peer.Read(unexpected[:]); n != 0 || err != io.EOF {
		t.Fatalf("later Type action reached EIS after cancellation: bytes=%x err=%v", unexpected[:n], err)
	}
}
