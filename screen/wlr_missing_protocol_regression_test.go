package screen

import (
	"context"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWlrReconnectWithoutScreencopyProtocolFailsClosed(t *testing.T) {
	b := NewWlrScreencopyBackendWithConnector("mock-reconnected-socket", func(string) (*wl.Context, error) { return &wl.Context{}, nil }, time.Minute)
	defer b.Close()
	img, err := b.Grab(context.Background(), image.Rectangle{})
	if img != nil || err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("missing screencopy manager accepted: img=%v err=%v", img, err)
	}
	if b.ctx != nil {
		t.Fatal("failed capture retained invalid Wayland connection")
	}
	for _, capture := range []struct {
		name string
		run  func() error
	}{
		{"full hash", func() error { _, err := b.GrabFullHash(context.Background()); return err }},
		{"region hash", func() error { _, err := b.GrabRegionHash(context.Background(), image.Rect(0, 0, 2, 2)); return err }},
	} {
		t.Run(capture.name, func(t *testing.T) {
			if err := capture.run(); err == nil || !strings.Contains(err.Error(), "protocol") {
				t.Fatalf("missing protocol accepted: %v", err)
			}
		})
	}
}
