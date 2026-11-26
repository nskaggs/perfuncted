package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/screen"
	"image"
	"image/png"
	"io"
	"time"
)

const watchMaxDirtyRects = 256

type watchChangeFrame struct {
	FrameID         uint64              `json:"frame_id"`
	GeometryVersion uint64              `json:"geometry_version"`
	BaseFrameID     *uint64             `json:"base_frame_id"`
	Timestamp       time.Time           `json:"timestamp"`
	ScreenRect      image.Rectangle     `json:"screen_rect"`
	ImageBounds     image.Rectangle     `json:"image_bounds"`
	DirtyRects      []image.Rectangle   `json:"dirty_rects"`
	Pixels          []watchPixelPayload `json:"pixels,omitempty"`
	FullFrame       bool                `json:"full_frame"`
}

func watchScreenChanges(ctx context.Context, out io.Writer, rect image.Rectangle, poll time.Duration, capture func(context.Context, image.Rectangle) (screen.Capture, error), includePixels bool) error {
	enc := json.NewEncoder(out)
	var previous screen.Capture
	var id, geometryVersion uint64
	timer := time.NewTimer(poll)
	stopAndDrainTimer(timer)
	defer timer.Stop()
	for ctx.Err() == nil {
		current, err := capture(ctx, rect)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		published, nextVersion, err := publishWatchChange(ctx, out, enc, previous, current, id, geometryVersion, includePixels)
		if err != nil {
			return err
		}
		geometryVersion = nextVersion
		if published {
			id++
		}
		previous = current
		timer.Reset(poll)
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
	return nil
}

func publishWatchChange(ctx context.Context, out io.Writer, enc *json.Encoder, previous, current screen.Capture, id, geometryVersion uint64, includePixels bool) (bool, uint64, error) {
	before := previous.Image
	if id == 0 {
		before = current.Image
	}
	dirty, resync, err := screen.DirtyRects(before, current.Image, watchMaxDirtyRects)
	if err != nil {
		return false, geometryVersion, err
	}
	bounds := current.Image.Bounds()
	if _, mappingErr := current.ScreenPoint(bounds.Min); mappingErr != nil {
		return false, geometryVersion, mappingErr
	}
	geometryChanged := id == 0 || current.ScreenRect != previous.ScreenRect || bounds != previous.Image.Bounds()
	if geometryChanged {
		geometryVersion++
	}
	full := geometryChanged || resync || (len(dirty) == 1 && dirty[0] == bounds)
	if !full && len(dirty) == 0 {
		return false, geometryVersion, nil
	}
	frame, err := buildWatchChangeFrame(current, dirty, id, geometryVersion, full)
	if err != nil {
		return false, geometryVersion, err
	}
	published, err := encodeWatchChange(ctx, out, enc, frame, current, dirty, includePixels)
	return published, geometryVersion, err
}

func buildWatchChangeFrame(current screen.Capture, dirty []image.Rectangle, id, geometryVersion uint64, full bool) (watchChangeFrame, error) {
	frame := watchChangeFrame{FrameID: id + 1, GeometryVersion: geometryVersion, Timestamp: time.Now().UTC(), ScreenRect: current.ScreenRect, ImageBounds: current.Image.Bounds(), FullFrame: full}
	if full {
		frame.DirtyRects = []image.Rectangle{current.ScreenRect}
		return frame, nil
	}
	frame.BaseFrameID = &id
	frame.DirtyRects = make([]image.Rectangle, 0, len(dirty))
	for _, r := range dirty {
		lo, err := current.ScreenPoint(r.Min)
		if err != nil {
			return frame, err
		}
		hi, err := current.ScreenPoint(r.Max.Sub(image.Pt(1, 1)))
		if err != nil {
			return frame, err
		}
		frame.DirtyRects = append(frame.DirtyRects, image.Rectangle{Min: lo, Max: hi.Add(image.Pt(1, 1))})
	}
	return frame, nil
}

func encodeWatchChange(ctx context.Context, out io.Writer, enc *json.Encoder, frame watchChangeFrame, current screen.Capture, dirty []image.Rectangle, includePixels bool) (bool, error) {
	if ctx.Err() != nil {
		return false, nil
	}
	if !includePixels {
		err := enc.Encode(frame)
		return err == nil, err
	}
	if frame.FullFrame {
		dirty = []image.Rectangle{current.Image.Bounds()}
	}
	err := writeWatchPixelFrame(ctx, out, frame, current, dirty, watchPixelFrameLimit)
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return false, nil
	}
	return err == nil, err
}

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
	checkCanceled func() error
	buf           bytes.Buffer
	limit         int
}

func (w *watchPNGWriter) Write(p []byte) (int, error) {
	if err := w.checkCanceled(); err != nil {
		return 0, err
	}
	if len(p) > w.limit-w.buf.Len() {
		return 0, fmt.Errorf("screen watch: pixel frame exceeds size limit")
	}
	return w.buf.Write(p)
}

func writeWatchPixelFrame(ctx context.Context, out io.Writer, frame watchChangeFrame, capture screen.Capture, crops []image.Rectangle, limit int) error {
	bounds := capture.Image.Bounds()
	if bounds.Dx() > watchPixelAxisLimit || bounds.Dy() > watchPixelAxisLimit || bounds.Dx() > watchPixelAreaLimit/bounds.Dy() {
		return fmt.Errorf("screen watch: pixel frame exceeds dimension limit")
	}
	remaining := limit
	for _, r := range crops {
		if cancelErr := ctx.Err(); cancelErr != nil {
			return cancelErr
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
		w := watchPNGWriter{checkCanceled: ctx.Err, limit: remaining / 4 * 3}
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
	if cancelErr := ctx.Err(); cancelErr != nil {
		return cancelErr
	}
	data = append(data, '\n')
	n, err := out.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
