//go:build linux

package input

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestRemoteDesktopAllAdvertisedDeviceLifecycleEventsAuthorizeRevocation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		id     func(*RemoteDesktopBackend) uint64
		opcode uint32
	}{
		{"keyboard device pause", func(b *RemoteDesktopBackend) uint64 { return b.sender.keyboardDev }, 8},
		{"button device removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.buttonDev }, 0},
		{"text device pause", func(b *RemoteDesktopBackend) uint64 { return b.sender.textDev }, 8},
		{"scroll device pause", func(b *RemoteDesktopBackend) uint64 { return b.sender.scrollDev }, 8},
		{"keyboard interface removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.keyboard }, 0},
		{"button interface removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.button }, 0},
		{"text interface removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.text }, 0},
		{"scroll interface removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.scroll }, 0},
		{"pointer device pause", func(b *RemoteDesktopBackend) uint64 { return b.sender.pointers[0].device }, 8},
		{"pointer interface removed", func(b *RemoteDesktopBackend) uint64 { return b.sender.pointers[0].pointer }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, client := newSyntheticRevocableRemoteDesktop(t)
			b.sender.pointers = []eiPointerDevice{{device: 500, pointer: 501}}
			id := tc.id(b)
			if id == 0 {
				t.Fatal("synthetic device identity unexpectedly zero")
			}
			event := eiMessage{object: id, opcode: tc.opcode}
			if !b.isEISLifecycleFailure(event) {
				t.Fatalf("device event %v not recognized as terminal", event)
			}
			client.messages <- event
			select {
			case <-b.revoked:
			case <-time.After(time.Second):
				t.Fatal("authorized input survived device loss")
			}
			if err := b.MouseDown(context.Background(), 1); !errors.Is(err, ErrRemoteDesktopRevoked) {
				t.Fatalf("revoked device admitted pointer input: %v", err)
			}
		})
	}
}

func TestRemoteDesktopUnrelatedDeviceAndUnsupportedOpcodeDoNotRevoke(t *testing.T) {
	b, _, client := newSyntheticRevocableRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 500, pointer: 501}}
	for _, event := range []eiMessage{
		{object: 0, opcode: 0},
		{object: 9999, opcode: 0},
		{object: 9999, opcode: 8},
		{object: 501, opcode: 8},
		{object: b.sender.keyboard, opcode: 18},
		{object: b.sender.keyboardDev, opcode: 18},
		{object: b.sender.seat, opcode: 18},
	} {
		if b.isEISLifecycleFailure(event) {
			t.Fatalf("unrelated EIS frame retired authorization: %+v", event)
		}
		client.messages <- event
	}
	select {
	case <-b.revoked:
		t.Fatal("unrelated device frame revoked portal")
	case <-time.After(25 * time.Millisecond):
	}
	if err := b.stateError(); err != nil {
		t.Fatalf("benign frames affected session: %v", err)
	}
}

func TestRemoteDesktopExtremeScrollCannotOverflowProtocolOrWrite(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"vertical max overflow", func() error { return b.ScrollDown(context.Background(), math.MaxInt) }},
		{"vertical min overflow", func() error { return b.ScrollUp(context.Background(), math.MaxInt) }},
		{"horizontal max overflow", func() error { return b.ScrollRight(context.Background(), math.MaxInt) }},
		{"horizontal min overflow", func() error { return b.ScrollLeft(context.Background(), math.MaxInt) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("out-of-range user input accepted")
			}
			if errors.Is(err, ErrRemoteDesktopRevoked) {
				t.Fatalf("invalid client input revoked authorized session: %v", err)
			}
		})
	}
	if err := b.stateError(); err != nil {
		t.Fatalf("overflow calculation mutated authorization: %v", err)
	}
}

func TestRemoteDesktopNilCauseRevokesExactlyOnce(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	first := b.revoke(nil)
	if !errors.Is(first, ErrRemoteDesktopRevoked) {
		t.Fatalf("nil-cause revocation: %v", first)
	}
	second := b.revoke(errors.New("second failure must not replace the first"))
	if !errors.Is(second, ErrRemoteDesktopRevoked) || second.Error() != first.Error() {
		t.Fatalf("revocation was not stable: first=%v second=%v", first, second)
	}
	select {
	case <-b.revoked:
	default:
		t.Fatal("revocation did not broadcast")
	}
}
