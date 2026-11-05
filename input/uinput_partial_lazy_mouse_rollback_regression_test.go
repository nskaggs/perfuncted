//go:build linux

package input

import (
	"context"
	"errors"
	"github.com/bendahl/uinput"
	"testing"
)

// Factories can return a partially initialized device alongside an error.
// Even though the mouse was never published, its file descriptors are owned
// by the caller and must be closed before returning the creation failure.
func TestUinputLazyMouseCreationErrorClosesPartialDevice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*UinputBackend) error
	}{{"scroll", func(b *UinputBackend) error { return b.ScrollDown(context.Background(), 1) }}, {"middle click", func(b *UinputBackend) error { return b.MouseDown(context.Background(), 2) }}} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newUinputActionBackend()
			b.mouse = nil
			partial := &recordingMouse{}
			creationErr := errors.New("synthetic partial mouse creation failure")
			old := createUinputMouse
			createUinputMouse = func(string, []byte) (uinput.Mouse, error) { return partial, creationErr }
			t.Cleanup(func() { createUinputMouse = old })
			if err := tc.invoke(b); !errors.Is(err, creationErr) {
				t.Fatalf("factory failure lost: %v", err)
			}
			if events := partial.eventsSnapshot(); len(events) != 1 || events[0] != "close" {
				t.Fatalf("partially created mouse not closed exactly once: %v", events)
			}
			if b.mouse != nil {
				t.Fatal("failed mouse creation retained unauthorized device")
			}
		})
	}
}

func TestUinputLazyMouseCreationPreservesRollbackFailure(t *testing.T) {
	b, _, _ := newUinputActionBackend()
	b.mouse = nil
	cleanupErr := errors.New("partial mouse close failed")
	partial := &recordingMouse{closeErr: cleanupErr}
	creationErr := errors.New("partial mouse create failed")
	old := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) { return partial, creationErr }
	t.Cleanup(func() { createUinputMouse = old })
	err := b.ScrollUp(context.Background(), 1)
	if !errors.Is(err, creationErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("creation and rollback errors not retained: %v", err)
	}
	if events := partial.eventsSnapshot(); len(events) != 1 || events[0] != "close" {
		t.Fatalf("partial mouse not closed: %v", events)
	}
}
