package find

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"math"
	"math/bits"

	"github.com/nskaggs/perfuncted/internal/contextutil"
)

// ErrUnverifiedCaptureGeometry reports that a live screenshot backend cannot
// prove the requested desktop rectangle is fully represented by its image.
var ErrUnverifiedCaptureGeometry = errors.New("find: unverified screenshot geometry")

// VerifiedTemplateCapture contains a screenshot plus backend-attested geometry.
// PixelScale is the exact integer number of image pixels per desktop unit.
// Backends that normalize captures to desktop dimensions report 1.
type VerifiedTemplateCapture struct {
	Image       image.Image
	DesktopRect image.Rectangle
	PixelScale  int
}

// VerifiedTemplateScreenshotter can prove that each returned image covers the
// complete requested desktop rectangle and reports its exact pixel scale.
// Implementations must reject clipped or ambiguous captures.
type VerifiedTemplateScreenshotter interface {
	GrabVerified(ctx context.Context, rect image.Rectangle) (VerifiedTemplateCapture, error)
}

// TemplateMatch is the best match of a reference image within a screenshot.
// Rect is in the search area's desktop coordinate space, not screenshot pixel
// coordinates. Confidence ranges from 0 (maximal color difference) to 1
// (identical visible reference pixels).
type TemplateMatch struct {
	Rect       image.Rectangle
	Confidence float64
}

// MatchTemplate captures searchArea once, then finds the highest-confidence
// location of reference anywhere inside it. It requires a backend that
// implements VerifiedTemplateScreenshotter so clipped or ambiguously scaled
// captures cannot produce false desktop coordinates. The matcher is pure Go.
//
// Confidence is 1 minus the reference-alpha-weighted mean absolute RGB channel
// difference divided by 255. Fully transparent reference pixels are ignored;
// partially transparent pixels receive proportional weight. It is a pixel
// similarity score, not OpenCV normalized cross-correlation, and does not
// compensate for rotation or template rescaling.
//
// A result is returned only when its confidence is >= minConfidence (0..1).
// Errors (including cancellation) never expose a partial match.
func MatchTemplate(
	ctx context.Context,
	sc Screenshotter,
	searchArea image.Rectangle,
	reference image.Image,
	minConfidence float64,
) (TemplateMatch, error) {
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return TemplateMatch{}, err
	}
	if err := contextErr(ctx); err != nil {
		return TemplateMatch{}, err
	}
	if err := validateTemplateInputs(searchArea, reference, minConfidence); err != nil {
		return TemplateMatch{}, err
	}
	verified, ok := sc.(VerifiedTemplateScreenshotter)
	if !ok {
		return TemplateMatch{}, fmt.Errorf("%w: backend %T does not attest captured desktop bounds", ErrUnverifiedCaptureGeometry, sc)
	}
	capture, err := verified.GrabVerified(ctx, searchArea)
	if err != nil {
		return TemplateMatch{}, fmt.Errorf("find: template grab: %w", err)
	}
	if err := contextErr(ctx); err != nil {
		return TemplateMatch{}, err
	}
	if err := validateVerifiedTemplateCapture(capture, searchArea); err != nil {
		return TemplateMatch{}, err
	}
	return MatchTemplateInImage(ctx, capture.Image, searchArea, reference, minConfidence)
}

func validateVerifiedTemplateCapture(capture VerifiedTemplateCapture, requested image.Rectangle) error {
	if capture.DesktopRect != requested {
		return fmt.Errorf("%w: backend covered %v, requested %v", ErrUnverifiedCaptureGeometry, capture.DesktopRect, requested)
	}
	if capture.PixelScale <= 0 {
		return fmt.Errorf("%w: invalid pixel scale %d", ErrUnverifiedCaptureGeometry, capture.PixelScale)
	}
	if err := checkImage(capture.Image, "verified template source"); err != nil {
		return fmt.Errorf("%w: %v", ErrUnverifiedCaptureGeometry, err)
	}
	bounds := capture.Image.Bounds()
	width, okW := validTemplateSpan(requested.Min.X, requested.Max.X)
	height, okH := validTemplateSpan(requested.Min.Y, requested.Max.Y)
	maxInt := int(^uint(0) >> 1)
	if !okW || !okH || width > maxInt/capture.PixelScale || height > maxInt/capture.PixelScale {
		return fmt.Errorf("%w: requested rectangle dimensions overflow pixel scale", ErrUnverifiedCaptureGeometry)
	}
	if bounds.Dx() != width*capture.PixelScale || bounds.Dy() != height*capture.PixelScale {
		return fmt.Errorf("%w: image is %dx%d pixels, expected %dx%d for %v at %dx scale", ErrUnverifiedCaptureGeometry, bounds.Dx(), bounds.Dy(), width*capture.PixelScale, height*capture.PixelScale, requested, capture.PixelScale)
	}
	return nil
}

// MatchTemplateInImage searches an already-captured screenshot without another
// backend call. Screenshot origins may differ from the desktop searchArea, and
// integral HiDPI upscaling is mapped back into desktop coordinates. Nonuniform,
// downscaled and ambiguous fractional-size screenshots fail closed; those may
// represent clipped captures. The caller is responsible for ensuring that its
// declared searchArea really is the area covered by src: the image-only API
// cannot independently verify capture provenance, even at integral scales.
// Context checks run during normalization, scanning, and pixel comparison.
func MatchTemplateInImage(
	ctx context.Context,
	src image.Image,
	searchArea image.Rectangle,
	reference image.Image,
	minConfidence float64,
) (TemplateMatch, error) {
	ctx = contextutil.Default(ctx)
	if err := contextErr(ctx); err != nil {
		return TemplateMatch{}, err
	}
	if err := validateTemplateInputs(searchArea, reference, minConfidence); err != nil {
		return TemplateMatch{}, err
	}
	source, needle, geometry, err := prepareTemplateImages(ctx, src, reference, searchArea)
	if err != nil {
		return TemplateMatch{}, err
	}
	samples, weightSum, err := buildTemplateSamples(ctx, source.Stride, needle)
	if err != nil {
		return TemplateMatch{}, err
	}
	return scanTemplate(ctx, source, samples, weightSum, geometry, minConfidence)
}

type templateGeometry struct {
	searchArea             image.Rectangle
	sourceW, sourceH       int
	referenceW, referenceH int
	screenW, screenH       int
}

type templateSample struct {
	offset  int
	r, g, b uint8
	weight  uint64
}

func prepareTemplateImages(
	ctx context.Context,
	src, reference image.Image,
	searchArea image.Rectangle,
) (*image.NRGBA, *image.NRGBA, templateGeometry, error) {
	var empty templateGeometry
	if err := checkImage(src, "template source"); err != nil {
		return nil, nil, empty, err
	}
	if !validPackedImageLayout(src) || !validPackedImageLayout(reference) {
		return nil, nil, empty, fmt.Errorf("find: malformed template or screenshot: %w", ErrInvalidFrame)
	}
	geometry, err := templateGeometryFor(src.Bounds(), reference.Bounds(), searchArea)
	if err != nil {
		return nil, nil, empty, err
	}
	source, err := packedTemplateImage(ctx, src)
	if err != nil {
		return nil, nil, empty, err
	}
	needle, err := packedTemplateImage(ctx, reference)
	if err != nil {
		return nil, nil, empty, err
	}
	return source, needle, geometry, nil
}

// templateGeometryFor verifies that a screenshot represents the requested
// desktop search area before any coordinates can be returned to a caller.
func templateGeometryFor(sb, rb, area image.Rectangle) (templateGeometry, error) {
	var empty templateGeometry
	sw, okSW := validTemplateSpan(sb.Min.X, sb.Max.X)
	sh, okSH := validTemplateSpan(sb.Min.Y, sb.Max.Y)
	rw, okRW := validTemplateSpan(rb.Min.X, rb.Max.X)
	rh, okRH := validTemplateSpan(rb.Min.Y, rb.Max.Y)
	dw, okDW := validTemplateSpan(area.Min.X, area.Max.X)
	dh, okDH := validTemplateSpan(area.Min.Y, area.Max.Y)
	if !okSW || !okSH || !okRW || !okRH || !okDW || !okDH {
		return empty, fmt.Errorf("find: template image or search rectangle has invalid bounds: %w", ErrInvalidFrame)
	}
	if sw < dw || sh < dh || !sameTemplateScale(sw, sh, dw, dh) {
		return empty, fmt.Errorf("find: screenshot bounds %v do not uniformly cover search area %v: %w", sb, area, ErrInvalidFrame)
	}
	// A fractional apparent scale is ambiguous without capture provenance:
	// a cropped 2x capture may look exactly like a complete 1.8x capture.
	// Accept only integral screenshot-to-desktop scales until the backend can
	// independently report its scale and actual covered desktop rectangle.
	// This deliberately rejects legitimate fractional HiDPI screenshots
	// rather than returning a potentially incorrect desktop coordinate.
	if sw%dw != 0 || sh%dh != 0 {
		return empty, fmt.Errorf("find: screenshot %v has ambiguous fractional scale relative to %v (cannot distinguish clipping): %w", sb, area, ErrInvalidFrame)
	}
	if rw > sw || rh > sh {
		return empty, fmt.Errorf("find: reference image larger than screenshot: %w", ErrNotFound)
	}
	// The worst-case retained scratch is bounded below 128 MiB:
	// one compact 16 Mi-pixel capture (64 MiB), one 2 Mi-pixel template
	// (8 MiB), and 2 Mi maximum alpha-visible samples (up to 48 MiB).
	const maxCapturePixels = 16 * 1024 * 1024
	const maxReferencePixels = 2 * 1024 * 1024
	if sw > maxCapturePixels/sh || rw > maxReferencePixels/rh {
		return empty, fmt.Errorf("find: template capture or reference exceeds supported pixel limit: %w", ErrInvalidFrame)
	}
	return templateGeometry{searchArea: area, sourceW: sw, sourceH: sh, referenceW: rw, referenceH: rh, screenW: dw, screenH: dh}, nil
}

// templateVisiblePixels counts only reference pixels with nonzero alpha.
// Allocating samples for the entire image before applying the mask could
// otherwise waste hundreds of MiB for mostly transparent templates.
func templateVisiblePixels(ctx context.Context, needle *image.NRGBA) (int, error) {
	bounds := needle.Bounds()
	visible := 0
	for y := 0; y < bounds.Dy(); y++ {
		row := y * needle.Stride
		for x := 0; x < bounds.Dx(); x++ {
			if x%1024 == 0 {
				if err := contextErr(ctx); err != nil {
					return 0, err
				}
			}
			if needle.Pix[row+x*4+3] > 0 {
				visible++
			}
		}
	}
	return visible, nil
}

func buildTemplateSamples(
	ctx context.Context,
	sourceStride int,
	needle *image.NRGBA,
) ([]templateSample, uint64, error) {
	visible, err := templateVisiblePixels(ctx, needle)
	if err != nil {
		return nil, 0, err
	}
	if visible == 0 {
		return nil, 0, fmt.Errorf("find: wholly transparent reference has no pixels to match: %w", ErrEmptyRegion)
	}
	width, height := needle.Bounds().Dx(), needle.Bounds().Dy()
	samples := make([]templateSample, 0, visible)
	var weightSum uint64
	for y := 0; y < height; y++ {
		row := y * needle.Stride
		for x := 0; x < width; x++ {
			if x%1024 == 0 {
				if err := contextErr(ctx); err != nil {
					return nil, 0, err
				}
			}
			off := row + x*4
			alpha := needle.Pix[off+3]
			if alpha == 0 {
				continue
			}
			samples = append(samples, templateSample{
				offset: y*sourceStride + x*4,
				r:      needle.Pix[off], g: needle.Pix[off+1], b: needle.Pix[off+2],
				weight: uint64(alpha),
			})
			weightSum += uint64(alpha)
		}
	}
	return samples, weightSum, nil
}

func scanTemplate(
	ctx context.Context,
	source *image.NRGBA,
	samples []templateSample,
	weightSum uint64,
	geometry templateGeometry,
	minConfidence float64,
) (TemplateMatch, error) {
	maxDiff := float64(weightSum) * 3 * 255
	bestConfidence := minConfidence
	found := false
	var bestRect image.Rectangle
	const cancelStride = 128
	for y := 0; y <= geometry.sourceH-geometry.referenceH; y++ {
		if err := contextErr(ctx); err != nil {
			return TemplateMatch{}, err
		}
		for x := 0; x <= geometry.sourceW-geometry.referenceW; x++ {
			if x%cancelStride == 0 {
				if err := contextErr(ctx); err != nil {
					return TemplateMatch{}, err
				}
			}
			start := y*source.Stride + x*4
			// Floating-point multiplication can round the exact integer loss
			// budget slightly below its mathematical value. Round the prune
			// budget upward, then apply the precise confidence check below.
			budget := math.Ceil((1 - bestConfidence) * maxDiff)
			diff, rejected, err := templateCandidateDiff(ctx, source.Pix, start, samples, budget)
			if err != nil {
				return TemplateMatch{}, err
			}
			if rejected {
				continue
			}
			confidence := 1 - float64(diff)/maxDiff
			if confidence < minConfidence || (found && confidence <= bestConfidence) {
				continue
			}
			bestConfidence, found = confidence, true
			bestRect = templateScreenRect(x, y, geometry)
			if confidence == 1 {
				if err := contextErr(ctx); err != nil {
					return TemplateMatch{}, err
				}
				return TemplateMatch{Rect: bestRect, Confidence: confidence}, nil
			}
		}
	}
	if err := contextErr(ctx); err != nil {
		return TemplateMatch{}, err
	}
	if !found {
		return TemplateMatch{}, fmt.Errorf("%w: template confidence below %.4f", ErrNotFound, minConfidence)
	}
	return TemplateMatch{Rect: bestRect, Confidence: bestConfidence}, nil
}

func templateCandidateDiff(ctx context.Context, pixels []byte, start int, samples []templateSample, budget float64) (uint64, bool, error) {
	var diff uint64
	for i, sample := range samples {
		if i > 0 && i%1024 == 0 {
			if err := contextErr(ctx); err != nil {
				return 0, false, err
			}
		}
		p := start + sample.offset
		diff += sample.weight * uint64(abs(int(pixels[p])-int(sample.r))+
			abs(int(pixels[p+1])-int(sample.g))+
			abs(int(pixels[p+2])-int(sample.b)))
		if float64(diff) > budget {
			return 0, true, nil
		}
	}
	return diff, false, nil
}

func templateScreenRect(x, y int, geometry templateGeometry) image.Rectangle {
	area := geometry.searchArea
	return image.Rect(
		area.Min.X+int(scaleTemplateOffset(uint64(x), geometry.sourceW, geometry.screenW, false)),
		area.Min.Y+int(scaleTemplateOffset(uint64(y), geometry.sourceH, geometry.screenH, false)),
		area.Min.X+int(scaleTemplateOffset(uint64(x+geometry.referenceW), geometry.sourceW, geometry.screenW, true)),
		area.Min.Y+int(scaleTemplateOffset(uint64(y+geometry.referenceH), geometry.sourceH, geometry.screenH, true)),
	)
}

func validateTemplateInputs(searchArea image.Rectangle, reference image.Image, minConfidence float64) error {
	if math.IsNaN(minConfidence) || math.IsInf(minConfidence, 0) || minConfidence < 0 || minConfidence > 1 {
		return fmt.Errorf("find: min confidence must be finite and in [0,1], got %g", minConfidence)
	}
	if searchArea.Empty() {
		return fmt.Errorf("find: template search area must be nonempty: %w", ErrEmptyRegion)
	}
	if err := checkImage(reference, "template reference"); err != nil {
		return err
	}
	if reference.Bounds().Empty() {
		return fmt.Errorf("find: template reference must be nonempty: %w", ErrEmptyRegion)
	}
	return nil
}

func validTemplateSpan(minimum, maximum int) (int, bool) {
	if maximum <= minimum {
		return 0, false
	}
	span := uint64(maximum) - uint64(minimum)
	if span > uint64(int(^uint(0)>>1)) {
		return 0, false
	}
	return int(span), true
}

func sameTemplateScale(imageWidth, imageHeight, screenWidth, screenHeight int) bool {
	hi1, lo1 := bits.Mul64(uint64(imageWidth), uint64(screenHeight))
	hi2, lo2 := bits.Mul64(uint64(imageHeight), uint64(screenWidth))
	return hi1 == hi2 && lo1 == lo2
}

func scaleTemplateOffset(offset uint64, from, to int, roundUp bool) uint64 {
	hi, lo := bits.Mul64(offset, uint64(to))
	value, remainder := bits.Div64(hi, lo, uint64(from))
	if roundUp && remainder != 0 {
		value++
	}
	return value
}

func packedTemplateImage(ctx context.Context, img image.Image) (*image.NRGBA, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	// Rebase potentially nonzero image bounds to a compact zero-origin frame.
	compact := image.Rect(0, 0, bounds.Dx(), bounds.Dy())
	out := image.NewNRGBA(compact)
	// Preserve original coordinates and periodically check cancellation
	// during conversion. Drawing a single large untrusted image would defer
	// cancellation until all pixels had already been read.
	// Bound both dimensions: a very wide single-row generic image must
	// not defer cancellation checks until every pixel has been consumed.
	const tileWidth, tileHeight = 256, 64
	for y := 0; y < compact.Dy(); y += tileHeight {
		for x := 0; x < compact.Dx(); x += tileWidth {
			if err := contextErr(ctx); err != nil {
				return nil, err
			}
			dst := image.Rect(x, y, min(x+tileWidth, compact.Dx()), min(y+tileHeight, compact.Dy()))
			draw.Draw(out, dst, img, image.Pt(bounds.Min.X+x, bounds.Min.Y+y), draw.Src)
		}
	}
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
