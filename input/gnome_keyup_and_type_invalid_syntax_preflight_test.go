package input

import (
	"context"
	"strings"
	"testing"
)

// The preflight boundary is shared by different input entry points. A bad
// release name or a malformed suffix in typed-key syntax must never dispatch
// even an otherwise valid prefix or alter a modifier the caller is holding.
func TestGnomeInvalidKeyReleaseAndTypedSequenceNeverDispatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*GnomeNativeBackend) error
	}{
		{"unknown release key", func(b *GnomeNativeBackend) error { return b.KeyUp(context.Background(), "not-a-real-key") }},
		{"invalid UTF8 release", func(b *GnomeNativeBackend) error { return b.KeyUp(context.Background(), string([]byte{0xff})) }},
		{"unclosed key syntax", func(b *GnomeNativeBackend) error { return b.Type(context.Background(), "prefix{ctrl down") }},
		{"empty key syntax", func(b *GnomeNativeBackend) error { return b.Type(context.Background(), "prefix{}") }},
		{"incomplete chord", func(b *GnomeNativeBackend) error { return b.Type(context.Background(), "prefix{ctrl+}") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &recordingGnomeBridge{}
			backend := &GnomeNativeBackend{bridge: bridge}
			if err := backend.KeyDown(context.Background(), "shift"); err != nil {
				t.Fatal(err)
			}
			bridge.events = nil
			err := tc.invoke(backend)
			if err == nil {
				t.Fatal("invalid GNOME input accepted")
			}
			if len(bridge.events) != 0 {
				t.Fatalf("invalid input reached desktop: %v (error %v)", bridge.events, err)
			}
			if !backend.held.shift || backend.held.ctrl {
				t.Fatalf("invalid input changed held-key ownership: %+v", backend.held)
			}
			if strings.Contains(tc.name, "UTF8") && !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("invalid encoding not explained: %v", err)
			}
		})
	}
}
