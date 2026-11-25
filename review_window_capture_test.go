package perfuncted

import (
	"context"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

func TestReviewBackgroundCaptureUnknownVisibilityIsNotForeground(t *testing.T) {
	info := window.Info{NativeID: "42", Title: "Editor", X: 10, Y: 20, W: 4, H: 3}
	backend := &windowPixmapScreenshotter{
		windowCaptureScreenshotter: &windowCaptureScreenshotter{},
		capture: func(context.Context, string) (screen.WindowPixmapCapture, error) {
			return screen.WindowPixmapCapture{Image: image.NewRGBA(image.Rect(0, 0, 4, 3)), ScreenRect: image.Rect(10, 20, 14, 23), Visibility: screen.WindowPixmapUnknown, Foreground: true, ForegroundKnown: true}, nil
		},
	}
	session := NewSessionForTesting(backend, nil, newHandleWindowManager(info), nil, nil)
	defer session.Close()
	target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
	if err != nil {
		t.Fatal(err)
	}
	got, err := session.Screen.CaptureWindowBackground(context.Background(), target)
	if err != nil || got.State != WindowCaptureStateUnknown {
		t.Fatalf("state=%q err=%v; focus does not prove visibility", got.State, err)
	}
}
