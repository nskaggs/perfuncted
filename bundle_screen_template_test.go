package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/find"
)

type templateBundleScreenshotter struct {
	frame  image.Image
	err    error
	grabFn func(context.Context, image.Rectangle) (image.Image, error)
}

func (s *templateBundleScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if s.grabFn != nil {
		return s.grabFn(ctx, rect)
	}
	return s.frame, s.err
}

func (s *templateBundleScreenshotter) GrabVerified(ctx context.Context, rect image.Rectangle) (find.VerifiedTemplateCapture, error) {
	img, err := s.Grab(ctx, rect)
	if err != nil {
		return find.VerifiedTemplateCapture{}, err
	}
	if img == nil || rect.Empty() || img.Bounds().Dx()%rect.Dx() != 0 || img.Bounds().Dy()%rect.Dy() != 0 {
		return find.VerifiedTemplateCapture{}, find.ErrUnverifiedCaptureGeometry
	}
	scaleX := img.Bounds().Dx() / rect.Dx()
	scaleY := img.Bounds().Dy() / rect.Dy()
	if scaleX <= 0 || scaleX != scaleY {
		return find.VerifiedTemplateCapture{}, find.ErrUnverifiedCaptureGeometry
	}
	return find.VerifiedTemplateCapture{Image: img, DesktopRect: rect, PixelScale: scaleX}, nil
}

func (*templateBundleScreenshotter) Close() error { return nil }
func (*templateBundleScreenshotter) GrabFullHash(context.Context) (uint32, error) {
	return 0, nil
}
func (*templateBundleScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func TestScreenBundleFindTemplateMapsHiDPIResultToDesktop(t *testing.T) {
	const scale = 2
	region := image.Rect(100, 200, 108, 208)
	frame := image.NewNRGBA(image.Rect(0, 0, region.Dx()*scale, region.Dy()*scale))
	template := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < template.Bounds().Dy(); y++ {
		for x := 0; x < template.Bounds().Dx(); x++ {
			pixel := color.NRGBA{R: uint8(30 + x*20), G: uint8(60 + y*20), B: uint8(90 + x + y), A: 255}
			template.SetNRGBA(x, y, pixel)
			frame.SetNRGBA(6+x, 8+y, pixel)
		}
	}
	backend := &templateBundleScreenshotter{frame: frame}
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	got, err := session.Screen.FindTemplate(context.Background(), region, template, 1)
	if err != nil {
		t.Fatalf("FindTemplate: %v", err)
	}
	want := image.Rect(103, 204, 105, 206)
	if got.Rect != want || got.Confidence != 1 {
		t.Fatalf("FindTemplate = %+v, want rect %v at confidence 1", got, want)
	}
}

func TestScreenBundleFindTemplatePreservesCaptureFailuresAndAvailability(t *testing.T) {
	region := image.Rect(0, 0, 4, 4)
	template := image.NewRGBA(image.Rect(0, 0, 1, 1))
	for _, tc := range []struct {
		name    string
		backend *templateBundleScreenshotter
		wantErr error
	}{
		{name: "missing backend", wantErr: ErrUnavailable},
		{name: "capture error", backend: &templateBundleScreenshotter{err: os.ErrPermission}, wantErr: os.ErrPermission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := NewSessionForTesting(tc.backend, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			got, err := session.Screen.FindTemplate(context.Background(), region, template, 0.95)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("FindTemplate error = %v, want %v", err, tc.wantErr)
			}
			if got != (find.TemplateMatch{}) {
				t.Fatalf("failed operation exposed a match: %+v", got)
			}
		})
	}
}

func TestScreenBundleFindTemplateRejectsInvalidThresholdBeforeCapture(t *testing.T) {
	calls := 0
	backend := &templateBundleScreenshotter{grabFn: func(context.Context, image.Rectangle) (image.Image, error) {
		calls++
		return image.NewRGBA(image.Rect(0, 0, 4, 4)), nil
	}}
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	for _, threshold := range []float64{-0.01, 1.01} {
		if got, err := session.Screen.FindTemplate(context.Background(), image.Rect(0, 0, 4, 4), image.NewRGBA(image.Rect(0, 0, 1, 1)), threshold); err == nil || got != (find.TemplateMatch{}) {
			t.Fatalf("invalid threshold %g returned %+v, %v", threshold, got, err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid thresholds reached backend %d times", calls)
	}
}

type gatedTemplateImage struct {
	bounds  image.Rectangle
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (g *gatedTemplateImage) ColorModel() color.Model { return color.NRGBAModel }
func (g *gatedTemplateImage) Bounds() image.Rectangle { return g.bounds }
func (g *gatedTemplateImage) At(_, _ int) color.Color {
	g.once.Do(func() {
		close(g.started)
		<-g.release
	})
	return color.NRGBA{R: 50, G: 80, B: 110, A: 255}
}

func TestScreenBundleFindTemplateReturnsNoResultAfterCloseOrTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		close   bool
		timeout bool
		wantErr error
	}{
		{name: "session close", close: true, wantErr: context.Canceled},
		{name: "operation timeout", timeout: true, wantErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			operationContext := make(chan context.Context, 1)
			frame := &gatedTemplateImage{bounds: image.Rect(0, 0, 2, 2), started: started, release: release}
			backend := &templateBundleScreenshotter{grabFn: func(ctx context.Context, _ image.Rectangle) (image.Image, error) {
				operationContext <- ctx
				return frame, nil
			}}
			session := NewSessionForTesting(backend, nil, nil, nil, nil)
			if tc.timeout {
				session.config.Timeouts = TimeoutPolicy{Medium: 20 * time.Millisecond}
			}
			result := make(chan struct {
				match find.TemplateMatch
				err   error
			}, 1)
			go func() {
				match, err := session.Screen.FindTemplate(context.Background(), frame.bounds, image.NewNRGBA(image.Rect(0, 0, 1, 1)), 0.95)
				result <- struct {
					match find.TemplateMatch
					err   error
				}{match: match, err: err}
			}()
			<-started
			captureContext := <-operationContext
			if tc.close {
				if err := session.Close(); err != nil {
					t.Fatalf("close session: %v", err)
				}
			}
			<-captureContext.Done()
			close(release)
			got := <-result
			if !errors.Is(got.err, tc.wantErr) || got.match != (find.TemplateMatch{}) {
				t.Fatalf("FindTemplate after revocation = %+v, %v; want no match and %v", got.match, got.err, tc.wantErr)
			}
			_ = session.Close()
		})
	}
}
