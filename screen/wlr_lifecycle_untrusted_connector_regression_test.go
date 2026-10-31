package screen

import (
	"context"
	"github.com/nskaggs/perfuncted/internal/wl"
	"image"
	"strings"
	"testing"
	"time"
)

func TestWlrResolutionFailsAfterBackendCloseEvenWhenDimensionsCached(t *testing.T) {
	b := &WlrScreencopyBackend{pW: 1920, pH: 1080}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if w, h, err := b.ResolutionWithContext(context.Background()); err == nil || w != 0 || h != 0 {
		t.Fatalf("closed backend leaked stale resolution %dx%d: %v", w, h, err)
	}
}
func TestWlrUntrustedConnectorCannotReturnNilConnectionAsSuccess(t *testing.T) {
	b := newWlrScreencopyBackendWithContextConnector("synthetic-nil", func(context.Context, string) (*wl.Context, error) { return nil, nil }, time.Hour)
	defer b.Close()
	if img, err := b.Grab(context.Background(), image.Rectangle{}); img != nil || err == nil || !strings.Contains(err.Error(), "connection") {
		t.Fatalf("connector nil-success reached capture: img=%v err=%v", img, err)
	}
}
