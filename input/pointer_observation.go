package input

import (
	"context"
	"fmt"
	"time"
)

// PointerCoordinateSpaceRootPixels identifies X11 root framebuffer pixels.
const PointerCoordinateSpaceRootPixels CoordinateSpaceKind = "x11-root-pixels"

// PointerObservation describes a fresh provider observation, not an injection target.
// X11 root pixels use native 1:1 scale, not a logical-to-output transform.
type PointerObservation struct {
	X               int                 `json:"x"`
	Y               int                 `json:"y"`
	CoordinateSpace CoordinateSpaceInfo `json:"coordinateSpace"`
	Provider        string              `json:"provider"`
	ObservedAt      time.Time           `json:"observedAt"`
}

// PointerObserver is implemented only by providers with an authoritative read contract.
type PointerObserver interface {
	ObservePointer(context.Context) (PointerObservation, error)
}

// Validate checks the observation-specific coordinate contract and provenance.
func (p PointerObservation) Validate() error {
	if p.Provider == "" || p.ObservedAt.IsZero() {
		return fmt.Errorf("input: missing pointer observation provenance")
	}
	if p.CoordinateSpace.Kind == PointerCoordinateSpaceRootPixels {
		if p.Provider != "x11-root" || p.CoordinateSpace.ScaleX != 1 || p.CoordinateSpace.ScaleY != 1 {
			return fmt.Errorf("input: invalid X11 root pixel metadata")
		}
		return nil
	}
	return p.CoordinateSpace.Validate()
}
