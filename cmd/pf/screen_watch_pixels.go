package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"

	"github.com/nskaggs/perfuncted"
)

const watchPixelFrameLimit = 16 << 20
const watchPixelAxisLimit = 16384
const watchPixelAreaLimit = 16 << 20

type watchPixelPayload struct {
	Encoding        string          `json:"encoding"`
	ImageBounds     image.Rectangle `json:"image_bounds"`
	Width           int             `json:"width"`
	Height          int             `json:"height"`
	PixelScale      float64         `json:"pixel_scale"`
	GeometryVersion uint64          `json:"geometry_version"`
	Data            []byte          `json:"data"`
}

// The writer bounds accumulated PNG bytes; bytes.Buffer capacity is bounded by its growth policy.
// PNG's own row workspace is bounded by the axis limit.
type watchPNGWriter struct {
	ctx   context.Context
	buf   bytes.Buffer
	limit int
}

func (w *watchPNGWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > w.limit-w.buf.Len() {
		return 0, fmt.Errorf("screen watch: pixel frame exceeds size limit")
	}
	return w.buf.Write(p)
}

func writeWatchPixelFrame(ctx context.Context, out io.Writer, frame watchChangeFrame, capture perfuncted.Capture, crops []image.Rectangle, limit int) error {
	bounds := capture.Image.Bounds()
	if bounds.Dx() > watchPixelAxisLimit || bounds.Dy() > watchPixelAxisLimit || bounds.Dx() > watchPixelAreaLimit/bounds.Dy() {
		return fmt.Errorf("screen watch: pixel frame exceeds dimension limit")
	}
	remaining := limit
	for _, r := range crops {
		if err := ctx.Err(); err != nil {
			return err
		}
		var crop image.Image
		switch img := capture.Image.(type) {
		case *image.RGBA:
			crop = img.SubImage(r)
		case *image.NRGBA:
			crop = img.SubImage(r)
		default:
			return fmt.Errorf("screen watch: unsupported pixel layout")
		}
		w := watchPNGWriter{ctx: ctx, limit: remaining / 4 * 3}
		if err := png.Encode(&w, crop); err != nil {
			return err
		}
		remaining -= base64.StdEncoding.EncodedLen(w.buf.Len())
		frame.Pixels = append(frame.Pixels, watchPixelPayload{
			Encoding: "png+base64", ImageBounds: r, Width: r.Dx(), Height: r.Dy(),
			PixelScale:      float64(bounds.Dx()) / float64(capture.ScreenRect.Dx()),
			GeometryVersion: frame.GeometryVersion, Data: w.buf.Bytes(),
		})
	}
	// Payload bytes are already bounded cumulatively. Metadata has bounded
	// rectangle count; marshal once and check the exact wire length.
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(data) >= limit {
		return fmt.Errorf("screen watch: pixel frame exceeds size limit")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data = append(data, '\n')
	n, err := out.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
