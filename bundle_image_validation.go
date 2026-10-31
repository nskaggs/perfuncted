package perfuncted

import (
	"fmt"
	"image"

	"github.com/nskaggs/perfuncted/internal/util"
)

// validateCaptureImageLayout rejects malformed built-in packed image buffers
// before passing them to pixel accessors or PNG encoding. A remote capture
// backend must not be able to panic the caller using a truncated Pix slice.
func validateCaptureImageLayout(img image.Image) error {
	if util.IsNil(img) {
		return fmt.Errorf("screen: invalid capture: nil image")
	}
	switch frame := img.(type) {
	case *image.RGBA:
		return validatePackedCaptureRows(frame.Rect, frame.Stride, frame.Pix, 4)
	case *image.NRGBA:
		return validatePackedCaptureRows(frame.Rect, frame.Stride, frame.Pix, 4)
	case *image.RGBA64:
		return validatePackedCaptureRows(frame.Rect, frame.Stride, frame.Pix, 8)
	case *image.NRGBA64:
		return validatePackedCaptureRows(frame.Rect, frame.Stride, frame.Pix, 8)
	case *image.Gray:
		return validatePackedCaptureRows(frame.Rect, frame.Stride, frame.Pix, 1)
	case *image.Gray16:
		return validatePackedCaptureRows(frame.Rect, frame.Stride, frame.Pix, 2)
	case *image.Alpha:
		return validatePackedCaptureRows(frame.Rect, frame.Stride, frame.Pix, 1)
	case *image.Alpha16:
		return validatePackedCaptureRows(frame.Rect, frame.Stride, frame.Pix, 2)
	case *image.Paletted:
		return validatePackedCaptureRows(frame.Rect, frame.Stride, frame.Pix, 1)
	case *image.CMYK:
		return validatePackedCaptureRows(frame.Rect, frame.Stride, frame.Pix, 4)
	case *image.YCbCr:
		return validateYCbCrCapturePlanes(frame)
	case *image.NYCbCrA:
		if err := validateYCbCrCapturePlanes(&frame.YCbCr); err != nil {
			return err
		}
		return validatePackedCaptureRows(frame.Rect, frame.AStride, frame.A, 1)
	default:
		return nil
	}
}

func validateYCbCrCapturePlanes(frame *image.YCbCr) error {
	if err := validatePackedCaptureRows(frame.Rect, frame.YStride, frame.Y, 1); err != nil {
		return err
	}
	width, height := frame.Rect.Dx(), frame.Rect.Dy()
	if width == 0 || height == 0 {
		return nil
	}
	xSubsample, ySubsample := 1, 1
	switch frame.SubsampleRatio {
	case image.YCbCrSubsampleRatio422:
		xSubsample = 2
	case image.YCbCrSubsampleRatio420:
		xSubsample, ySubsample = 2, 2
	case image.YCbCrSubsampleRatio440:
		ySubsample = 2
	case image.YCbCrSubsampleRatio411:
		xSubsample = 4
	case image.YCbCrSubsampleRatio410:
		xSubsample, ySubsample = 4, 2
	}
	chromaWidth := (frame.Rect.Max.X-1)/xSubsample - frame.Rect.Min.X/xSubsample + 1
	chromaHeight := (frame.Rect.Max.Y-1)/ySubsample - frame.Rect.Min.Y/ySubsample + 1
	chromaBounds := image.Rect(0, 0, chromaWidth, chromaHeight)
	if err := validatePackedCaptureRows(chromaBounds, frame.CStride, frame.Cb, 1); err != nil {
		return err
	}
	return validatePackedCaptureRows(chromaBounds, frame.CStride, frame.Cr, 1)
}

func validatePackedCaptureRows(rect image.Rectangle, stride int, pix []byte, bytesPerPixel int) error {
	width, height := rect.Dx(), rect.Dy()
	if width < 0 || height < 0 {
		return fmt.Errorf("screen: invalid capture bounds %v", rect)
	}
	if width == 0 || height == 0 {
		return nil
	}
	maxInt := int(^uint(0) >> 1)
	if width > maxInt/bytesPerPixel {
		return fmt.Errorf("screen: invalid capture width %d overflows pixel row", width)
	}
	bytesPerRow := width * bytesPerPixel
	if stride < bytesPerRow || len(pix) < bytesPerRow || (height-1) > (len(pix)-bytesPerRow)/stride {
		return fmt.Errorf("screen: invalid capture stride=%d bytes=%d bounds=%v", stride, len(pix), rect)
	}
	return nil
}
