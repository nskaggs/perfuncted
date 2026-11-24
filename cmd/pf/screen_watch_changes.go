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
	FrameID         uint64            `json:"frame_id"`
	GeometryVersion uint64            `json:"geometry_version"`
	BaseFrameID     *uint64           `json:"base_frame_id"`
	Timestamp       time.Time         `json:"timestamp"`
	ScreenRect      image.Rectangle   `json:"screen_rect"`
	ImageBounds     image.Rectangle   `json:"image_bounds"`
	DirtyRects      []image.Rectangle `json:"dirty_rects"`
	FullFrame       bool              `json:"full_frame"`
}

func watchScreenChanges(ctx context.Context, out io.Writer, rect image.Rectangle, poll time.Duration, capture func(context.Context, image.Rectangle) (perfuncted.Capture, error)) error {
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
		// Self-comparison on the first sample validates layout before mapping.
		before := previous.Image
		if id == 0 {
			before = current.Image
		}
		dirty, resync, err := screen.DirtyRects(before, current.Image, watchMaxDirtyRects)
		if err != nil {
			return err
		}
		bounds := current.Image.Bounds()
		if _, err = current.ScreenPoint(bounds.Min); err != nil {
			return err
		}
		geometryChanged := id == 0 || current.ScreenRect != previous.ScreenRect || bounds != previous.Image.Bounds()
		if geometryChanged {
			geometryVersion++
		}
		full := geometryChanged || resync
		if full || len(dirty) > 0 {
			frame := watchChangeFrame{FrameID: id + 1, GeometryVersion: geometryVersion, Timestamp: time.Now().UTC(), ScreenRect: current.ScreenRect, ImageBounds: bounds, FullFrame: full}
			if full {
				frame.DirtyRects = []image.Rectangle{current.ScreenRect}
			} else {
				base := id
				frame.BaseFrameID = &base
				frame.DirtyRects = make([]image.Rectangle, 0, len(dirty))
				for _, r := range dirty {
					lo, err := current.ScreenPoint(r.Min)
					if err != nil {
						return err
					}
					hi, err := current.ScreenPoint(r.Max.Sub(image.Pt(1, 1)))
					if err != nil {
						return err
					}
					frame.DirtyRects = append(frame.DirtyRects, image.Rectangle{Min: lo, Max: hi.Add(image.Pt(1, 1))})
				}
			}
			if ctx.Err() != nil {
				return nil
			}
			if err := enc.Encode(frame); err != nil {
				return err
			}
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
