package main

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"io"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/screen"
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

func watchScreenChanges(ctx context.Context, out io.Writer, rect image.Rectangle, poll time.Duration, capture func(context.Context, image.Rectangle) (perfuncted.Capture, error), includePixels bool) error {
	enc := json.NewEncoder(out)
	var previous perfuncted.Capture
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

func publishWatchChange(ctx context.Context, out io.Writer, enc *json.Encoder, previous, current perfuncted.Capture, id, geometryVersion uint64, includePixels bool) (bool, uint64, error) {
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

func buildWatchChangeFrame(current perfuncted.Capture, dirty []image.Rectangle, id, geometryVersion uint64, full bool) (watchChangeFrame, error) {
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

func encodeWatchChange(ctx context.Context, out io.Writer, enc *json.Encoder, frame watchChangeFrame, current perfuncted.Capture, dirty []image.Rectangle, includePixels bool) (bool, error) {
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
