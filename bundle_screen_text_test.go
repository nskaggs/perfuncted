package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted/find"
)

type textBundleScreenshotter struct {
	image  image.Image
	err    error
	calls  int
	region image.Rectangle
}

type plainTextScreenshotter struct{ image image.Image }

func (s *plainTextScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return s.image, nil
}
func (*plainTextScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*plainTextScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*plainTextScreenshotter) Close() error { return nil }

func (s *textBundleScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	return s.image, s.err
}
func (*textBundleScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*textBundleScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*textBundleScreenshotter) Close() error { return nil }
func (s *textBundleScreenshotter) GrabVerified(ctx context.Context, rect image.Rectangle) (find.VerifiedTemplateCapture, error) {
	img, err := s.Grab(ctx, rect)
	if err != nil {
		return find.VerifiedTemplateCapture{}, err
	}
	if rect != s.region || img == nil || rect.Empty() || img.Bounds().Dx()%rect.Dx() != 0 || img.Bounds().Dy()%rect.Dy() != 0 {
		return find.VerifiedTemplateCapture{}, find.ErrUnverifiedCaptureGeometry
	}
	scaleX, scaleY := img.Bounds().Dx()/rect.Dx(), img.Bounds().Dy()/rect.Dy()
	if scaleX <= 0 || scaleX != scaleY {
		return find.VerifiedTemplateCapture{}, find.ErrUnverifiedCaptureGeometry
	}
	return find.VerifiedTemplateCapture{Image: img, DesktopRect: rect, PixelScale: scaleX}, nil
}

type bundleTextRecognizer struct {
	words []find.OCRWord
	err   error
	calls int
}

func (*bundleTextRecognizer) Name() string     { return "bundle-fixture" }
func (*bundleTextRecognizer) Language() string { return "eng" }
func (r *bundleTextRecognizer) Recognize(context.Context, image.Image) ([]find.OCRWord, error) {
	r.calls++
	return r.words, r.err
}

func TestScreenBundleFindTextUsesVerifiedCaptureAndSessionLifecycle(t *testing.T) {
	region := image.Rect(40, 50, 50, 60)
	frame := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	backend := &textBundleScreenshotter{image: frame, region: region}
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	confidence := 88.0
	provider := &bundleTextRecognizer{words: []find.OCRWord{{Text: "Continue", PixelRect: image.Rect(2, 4, 18, 16), Confidence: &confidence, Page: 1, Block: 1, Paragraph: 1, Line: 1, Word: 1}}}
	got, err := session.Screen.FindText(context.Background(), region, "Continue", find.TextOptions{Recognizer: provider})
	if err != nil {
		t.Fatal(err)
	}
	if backend.calls != 1 || provider.calls != 1 || len(got.Matches) != 1 || got.Matches[0].Rect != image.Rect(41, 52, 49, 58) {
		t.Fatalf("captures=%d provider calls=%d result=%+v", backend.calls, provider.calls, got)
	}
}

func TestScreenBundleFindTextRejectsMissingProviderBeforeCapture(t *testing.T) {
	region := image.Rect(0, 0, 4, 4)
	frame := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	backend := &textBundleScreenshotter{image: frame, region: region}
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	_, err := session.Screen.FindText(context.Background(), region, "text", find.TextOptions{})
	if !errors.Is(err, find.ErrOCRProviderUnavailable) || backend.calls != 0 {
		t.Fatalf("error=%v captures=%d", err, backend.calls)
	}
}

func TestScreenBundleFindTextRejectsBackendWithoutVerifiedGeometry(t *testing.T) {
	region := image.Rect(0, 0, 4, 4)
	session := NewSessionForTesting(&plainTextScreenshotter{image: image.NewNRGBA(region)}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	provider := &bundleTextRecognizer{}
	_, err := session.Screen.FindText(context.Background(), region, "text", find.TextOptions{Recognizer: provider})
	if !errors.Is(err, find.ErrOCRGeometry) || provider.calls != 0 {
		t.Fatalf("error=%v recognition calls=%d", err, provider.calls)
	}
}
