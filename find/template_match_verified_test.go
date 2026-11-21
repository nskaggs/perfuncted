package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

type templateVerifiedFixture struct {
	capture VerifiedTemplateCapture
	err     error
	calls   int
}

func (s *templateVerifiedFixture) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return s.capture.Image, s.err
}

func (s *templateVerifiedFixture) GrabVerified(context.Context, image.Rectangle) (VerifiedTemplateCapture, error) {
	s.calls++
	return s.capture, s.err
}

type templateUnverifiedFixture struct {
	frame image.Image
	calls int
}

func (s *templateUnverifiedFixture) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	return s.frame, nil
}

func TestMatchTemplateRequiresTrustedCaptureAndRejectsHiddenScaleClipping(t *testing.T) {
	area := image.Rect(-1, -1, 9, 9)
	reference := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	reference.SetNRGBA(0, 0, color.NRGBA{R: 250, G: 20, B: 30, A: 255})

	t.Run("legacy screenshotter fails closed before capture", func(t *testing.T) {
		backend := &templateUnverifiedFixture{frame: image.NewRGBA(image.Rect(0, 0, 20, 20))}
		got, err := MatchTemplate(context.Background(), backend, area, reference, 1)
		if !errors.Is(err, ErrUnverifiedCaptureGeometry) || got != (TemplateMatch{}) || backend.calls != 0 {
			t.Fatalf("unverified live capture = %+v, %v, calls=%d", got, err, backend.calls)
		}
	})

	t.Run("integer 2x crop cannot masquerade as complete capture", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(0, 0, 18, 18))
		frame.SetRGBA(0, 0, color.RGBA{R: 250, G: 20, B: 30, A: 255})
		backend := &templateVerifiedFixture{capture: VerifiedTemplateCapture{
			Image: frame, DesktopRect: area, PixelScale: 2,
		}}
		got, err := MatchTemplate(context.Background(), backend, area, reference, 1)
		if !errors.Is(err, ErrUnverifiedCaptureGeometry) || got != (TemplateMatch{}) {
			t.Fatalf("clipped verified capture = %+v, %v", got, err)
		}
	})

	t.Run("complete 2x capture maps to desktop coordinates", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(0, 0, 20, 20))
		frame.SetRGBA(8, 4, color.RGBA{R: 250, G: 20, B: 30, A: 255})
		backend := &templateVerifiedFixture{capture: VerifiedTemplateCapture{
			Image: frame, DesktopRect: area, PixelScale: 2,
		}}
		got, err := MatchTemplate(context.Background(), backend, area, reference, 1)
		if err != nil || got.Rect != image.Rect(3, 1, 4, 2) || got.Confidence != 1 {
			t.Fatalf("verified 2x capture = %+v, %v", got, err)
		}
	})
}

func TestMatchTemplateRejectsIncorrectVerifiedDesktopOrigin(t *testing.T) {
	area := image.Rect(10, 20, 14, 24)
	backend := &templateVerifiedFixture{capture: VerifiedTemplateCapture{
		Image: image.NewRGBA(image.Rect(0, 0, 4, 4)), DesktopRect: image.Rect(9, 20, 13, 24), PixelScale: 1,
	}}
	got, err := MatchTemplate(context.Background(), backend, area, image.NewRGBA(image.Rect(0, 0, 1, 1)), 0)
	if !errors.Is(err, ErrUnverifiedCaptureGeometry) || got != (TemplateMatch{}) {
		t.Fatalf("wrong-origin capture = %+v, %v", got, err)
	}
}
