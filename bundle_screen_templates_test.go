package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"testing"

	"github.com/nskaggs/perfuncted/find"
)

func TestScreenBundleFindTemplatesResultFailuresAndAuthority(t *testing.T) {
	area := image.Rect(0, 0, 3, 1)
	ref := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	ref.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	frame := image.NewNRGBA(area)
	frame.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	frame.SetNRGBA(2, 0, color.NRGBA{R: 255, A: 255})
	session := NewSessionForTesting(&templateBundleScreenshotter{frame: frame}, nil, nil, nil, nil)
	got, err := session.Screen.FindTemplates(context.Background(), area, ref, 1, 64)
	if err != nil || len(got) != 2 || got[0].Rect.Min.X != 0 || got[1].Rect.Min.X != 2 {
		t.Fatalf("matches=%v err=%v", got, err)
	}
	no, err := session.Screen.FindTemplates(context.Background(), area, ref, 1, 0)
	if !errors.Is(err, find.ErrInvalidFrame) || no != nil {
		t.Fatalf("invalid=%v %v", no, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	no, err = session.Screen.FindTemplates(ctx, area, ref, 1, 64)
	if !errors.Is(err, context.Canceled) || no != nil {
		t.Fatalf("cancelled=%v %v", no, err)
	}
	_ = session.Close()
	no, err = session.Screen.FindTemplates(context.Background(), area, ref, 1, 64)
	if err == nil || no != nil {
		t.Fatalf("closed=%v %v", no, err)
	}
	missing := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = missing.Close() })
	no, err = missing.Screen.FindTemplates(context.Background(), area, ref, 1, 64)
	if !errors.Is(err, ErrUnavailable) || no != nil {
		t.Fatalf("unavailable=%v %v", no, err)
	}
	denied := NewSessionForTesting(&templateBundleScreenshotter{err: os.ErrPermission}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = denied.Close() })
	no, err = denied.Screen.FindTemplates(context.Background(), area, ref, 1, 64)
	if !errors.Is(err, os.ErrPermission) || no != nil {
		t.Fatalf("denied=%v %v", no, err)
	}
}

type closingTemplateFrame struct {
	image.Image
	session *Session
}

func (f *closingTemplateFrame) At(x, y int) color.Color {
	f.session.lifecycleMu.Lock()
	f.session.closed = true
	f.session.lifecycleMu.Unlock()
	return f.Image.At(x, y)
}
func TestScreenBundleFindTemplatesChecksLateClosedSession(t *testing.T) {
	region := image.Rect(0, 0, 1, 1)
	frame := image.NewNRGBA(region)
	frame.SetNRGBA(0, 0, color.NRGBA{A: 255, R: 255})
	backend := &templateBundleScreenshotter{}
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	backend.frame = &closingTemplateFrame{Image: frame, session: session}
	reference := image.NewNRGBA(region)
	reference.SetNRGBA(0, 0, color.NRGBA{A: 255, R: 255})
	result, err := session.Screen.FindTemplates(context.Background(), region, reference, 1, 1)
	session.lifecycleMu.Lock()
	session.closed = false // Restore synthetic state so Close runs its real cleanup.
	session.lifecycleMu.Unlock()
	t.Cleanup(func() { _ = session.Close() })
	if !errors.Is(err, ErrSessionClosed) || result != nil {
		t.Fatalf("late closure=%v %v", result, err)
	}
}
