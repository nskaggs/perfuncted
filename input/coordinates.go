package input

import (
	"context"
	"fmt"
	"math"
)

// CoordinateSpaceKind identifies the absolute coordinate space accepted by a
// pointer backend. It is deliberately separate from browser CSS/device-pixel
// concepts; callers must use the backend's declared space rather than infer a
// scale from a browser's devicePixelRatio.
type CoordinateSpaceKind string

const (
	// CoordinateSpaceLogical is a global logical/compositor coordinate space.
	CoordinateSpaceLogical CoordinateSpaceKind = "global-logical"
	// CoordinateSpacePhysical is a global output-pixel space. ScaleX and
	// ScaleY in CoordinateSpaceInfo convert logical screen units to it.
	CoordinateSpacePhysical CoordinateSpaceKind = "global-physical"
)

// CoordinateSpaceInfo describes the coordinate space accepted by an input
// backend. Scale values are authoritative backend metadata, expressed as
// backend units per global logical screen unit. Logical backends report 1,1.
type CoordinateSpaceInfo struct {
	Kind   CoordinateSpaceKind `json:"kind"`
	ScaleX float64             `json:"scaleX"`
	ScaleY float64             `json:"scaleY"`
}

// Validate rejects incomplete or unsupported coordinate reports. Unknown
// backends should omit PointerCoordinateSpace rather than return a guess.
func (info CoordinateSpaceInfo) Validate() error {
	if info.Kind != CoordinateSpaceLogical && info.Kind != CoordinateSpacePhysical {
		return fmt.Errorf("input: unknown pointer coordinate space %q", info.Kind)
	}
	if math.IsNaN(info.ScaleX) || math.IsInf(info.ScaleX, 0) || math.IsNaN(info.ScaleY) || math.IsInf(info.ScaleY, 0) || info.ScaleX <= 0 || info.ScaleY <= 0 {
		return fmt.Errorf("input: pointer coordinate scale must be positive, got %gx%g", info.ScaleX, info.ScaleY)
	}
	if info.Kind == CoordinateSpaceLogical && (info.ScaleX != 1 || info.ScaleY != 1) {
		return fmt.Errorf("input: logical pointer coordinate scale must be 1x1, got %gx%g", info.ScaleX, info.ScaleY)
	}
	return nil
}

// PointerCoordinateSpaceReporter is implemented only by backends that can
// prove the absolute coordinate space they accept. It is optional so legacy
// or compositor-agnostic backends fail closed instead of guessing.
type PointerCoordinateSpaceReporter interface {
	PointerCoordinateSpace(context.Context) (CoordinateSpaceInfo, error)
}
