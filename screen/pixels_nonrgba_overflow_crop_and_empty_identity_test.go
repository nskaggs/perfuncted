package screen

import (
	"image"
	"testing"
)

// External screenshot providers can return an ordinary NRGBA image while a
// caller supplies an enormous crop. Reject dimensions whose row or total
// byte counts overflow before allocating an output buffer.
func TestGenericImageCropRejectsUnallocatableRequestedDimensions(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for _, tc := range []struct {
		name string
		rect image.Rectangle
	}{
		{"unallocatable width", image.Rect(0, 0, maxInt, 1)},
		{"unallocatable height", image.Rect(0, 0, 1, maxInt)},
		{"unallocatable area", image.Rect(0, 0, maxInt/8, maxInt/8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cropped := cropImage(source, tc.rect)
			if cropped == nil || !cropped.Bounds().Empty() {
				t.Fatalf("untrusted huge crop allocated image: %+v", cropped)
			}
		})
	}
	if got := cropImage(source, image.Rectangle{}); got != source {
		t.Fatal("empty region changed source image identity")
	}
}

// Dimension validation must fail closed independently for the column count,
// row count and multiplication, rather than overflow and appear plausible.
func TestPixelRectDimensionValidationRejectsOverflowAndReversedBoundaries(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if w, h, ok := safePixelRectDimensions(image.Rectangle{}); !ok || w != 0 || h != 0 {
		t.Fatalf("empty rectangle invalid: %d %d %v", w, h, ok)
	}
	if _, _, ok := safePixelRectDimensions(image.Rect(0, 0, maxInt, 1)); ok {
		t.Fatal("overflowed row width accepted")
	}
	if _, _, ok := safePixelRectDimensions(image.Rect(0, 0, 1, maxInt)); ok {
		t.Fatal("overflowed total image allocation accepted")
	}
	if _, ok := safePixelDimension(2, 1); ok {
		t.Fatal("reversed span accepted")
	}
}
