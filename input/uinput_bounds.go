package input

import (
	"context"
	"fmt"
	"math"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
)

// validateUinputDimensions runs before opening /dev/uinput. Zero axes create
// a device which libinput rejects (min == max), despite the uinput ioctl
// returning success.
func validateUinputDimensions(width, height int32) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("input/uinput: invalid touchpad dimensions %dx%d (both must be positive)", width, height)
	}
	return nil
}

// uinputBoundsFromOutputs returns compositor-logical global bounds. It rejects
// layouts that need a negative origin: the virtual touchpad currently advertises
// [0,max] axes, and silently clipping negative coordinates would misclick.
func uinputBoundsFromOutputs(outputs []output.Info) (int32, int32, error) {
	var maxX, maxY int64
	found := false
	for _, o := range outputs {
		if !o.Available {
			continue
		}
		g := o.Geometry
		if g.X < 0 || g.Y < 0 || g.W <= 0 || g.H <= 0 {
			return 0, 0, fmt.Errorf("input/uinput: unsupported logical output geometry (%d,%d %dx%d)", g.X, g.Y, g.W, g.H)
		}
		x, y := int64(g.X)+int64(g.W), int64(g.Y)+int64(g.H)
		if x > math.MaxInt32 || y > math.MaxInt32 {
			return 0, 0, fmt.Errorf("input/uinput: output geometry exceeds input device range")
		}
		if x > maxX {
			maxX = x
		}
		if y > maxY {
			maxY = y
		}
		found = true
	}
	if !found || maxX == 0 || maxY == 0 {
		return 0, 0, fmt.Errorf("input/uinput: no valid logical output geometry to configure virtual touchpad")
	}
	return int32(maxX), int32(maxY), nil
}

var openUinputOutputLister = output.OpenRuntimeContext

// discoverUinputLogicalBounds closes the lister even if enumeration fails
// and only grants the single-output guarantee when metadata is authoritative.
func discoverUinputLogicalBounds(ctx context.Context, rt env.Runtime) (int32, int32, bool, error) {
	lister, err := openUinputOutputLister(ctx, rt)
	if err != nil {
		return 0, 0, false, fmt.Errorf("input/uinput: determine logical desktop dimensions: %w", err)
	}
	outputs, listErr := lister.List(ctx)
	closeErr := lister.Close()
	if listErr != nil {
		return 0, 0, false, fmt.Errorf("input/uinput: list outputs: %w", listErr)
	}
	if closeErr != nil {
		return 0, 0, false, fmt.Errorf("input/uinput: close output discovery: %w", closeErr)
	}
	width, height, err := uinputBoundsFromOutputs(outputs)
	if err != nil {
		return 0, 0, false, err
	}
	provenSingleOutput := len(outputs) == 1 && outputs[0].Available && outputs[0].Geometry.X == 0 && outputs[0].Geometry.Y == 0 && outputs[0].Geometry.W == int(width) && outputs[0].Geometry.H == int(height)
	return width, height, provenSingleOutput, nil
}

func newResolvedUinputBackend(ctx context.Context, rt env.Runtime, width, height int32) (Inputter, error) {
	provenSingleOutput := false
	if width == 0 && height == 0 {
		var err error
		width, height, provenSingleOutput, err = discoverUinputLogicalBounds(ctx, rt)
		if err != nil {
			return nil, err
		}
	}
	if err := validateUinputDimensions(width, height); err != nil {
		return nil, err
	}
	b, err := newUinputBackend(width, height)
	if err != nil {
		if usableInputter(b) {
			_ = b.Close()
		}
		return nil, err
	}
	if !usableInputter(b) {
		return nil, fmt.Errorf("input/uinput: constructor returned nil backend")
	}
	if real, ok := b.(*UinputBackend); ok {
		real.verifiedSingleLogicalOutput = provenSingleOutput
	}
	return b, nil
}
