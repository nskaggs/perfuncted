//go:build linux

package screen

import (
	"context"
	"image"
	"testing"
)

// Closing the KWin wrapper must not take ownership of an injected transport
// that has no private D-Bus connection. Its capture operations remain usable.
func TestKWinBackendCloseDoesNotRetireInjectedTransport(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	transport := &fakeKWinTransport{active: source, area: source}
	backend := &KWinShotBackend{transport: transport}
	if err := backend.Close(); err != nil {
		t.Fatalf("close injected transport wrapper: %v", err)
	}
	got, err := backend.Grab(context.Background(), image.Rectangle{})
	if err != nil || got == nil || got.Bounds() != source.Bounds() {
		t.Fatalf("closing wrapper retired borrowed transport: image=%v err=%v", got, err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("repeated close injected transport wrapper: %v", err)
	}
}
