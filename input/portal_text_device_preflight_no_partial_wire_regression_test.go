//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// A portal may advertise the text interface object before the matching
// device is selected or resumed. A Type batch must reject that incomplete
// capability before any earlier key, modifier, or pointer-independent text
// events are injected into another application.
func TestRemoteDesktopTypeDoesNotPartiallyInjectWhenTextDeviceMissing(t *testing.T) {
	for _, tc := range []struct {
		name, sequence string
		disable        func(*eiSender)
	}{
		{"key prefix then missing text device", "{enter}private", func(s *eiSender) { s.textDev = 0 }},
		{"modifier prefix then missing text device", "{ctrl+a}private", func(s *eiSender) { s.textDev = 0 }},
		{"key prefix then missing text object", "{enter}private", func(s *eiSender) { s.text = 0 }},
		{"pure literal missing text device", "private", func(s *eiSender) { s.textDev = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			tc.disable(b.sender)
			if err := b.Type(context.Background(), tc.sequence); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("incomplete text authority accepted or wrong error: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("rejected batch changed held keys: %v", b.heldKeys)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("validation revoked otherwise live input session: %v", err)
			}
		})
	}
}
