// Package output lists display outputs exposed by X11 and Wayland sessions.
package output

import (
	"context"
	"fmt"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// Geometry describes an output rectangle in compositor coordinates.
type Geometry struct {
	// X is the left coordinate in compositor space.
	X int `json:"x"`
	// Y is the top coordinate in compositor space.
	Y int `json:"y"`
	// W is the output width in compositor space.
	W int `json:"w"`
	// H is the output height in compositor space.
	H int `json:"h"`
}

// Info describes a read-only display output.
type Info struct {
	// Name is the compositor-provided output name, when available.
	Name string `json:"name,omitempty"`
	// Backend identifies the backend that reported the output.
	Backend string `json:"backend"`
	// Geometry is the output rectangle in compositor coordinates.
	Geometry Geometry `json:"geometry"`
	// ResolutionW is the physical pixel width, when available.
	ResolutionW int `json:"resolution_w,omitempty"`
	// ResolutionH is the physical pixel height, when available.
	ResolutionH int `json:"resolution_h,omitempty"`
	// Scale is the compositor scale factor.
	// For fractional scales, Scale is zero and ScaleNumerator/ScaleDenominator
	// carry the exact ratio when the backend can determine it.
	Scale int `json:"scale,omitempty"`
	// ScaleNumerator and ScaleDenominator represent the logical-to-physical
	// scale as a reduced positive rational number when available.
	ScaleNumerator   int `json:"scale_numerator,omitempty"`
	ScaleDenominator int `json:"scale_denominator,omitempty"`
	// PhysicalW is the physical width in millimeters, when available.
	PhysicalW int `json:"physical_w,omitempty"`
	// PhysicalH is the physical height in millimeters, when available.
	PhysicalH int `json:"physical_h,omitempty"`
	// Make is the output manufacturer, when available.
	Make string `json:"make,omitempty"`
	// Model is the output model, when available.
	Model string `json:"model,omitempty"`
	// Description is the compositor-provided output description.
	Description string `json:"description,omitempty"`
	// Primary reports whether the output is the primary display.
	Primary bool `json:"primary,omitempty"`
	// Available reports whether the output can currently be queried.
	Available bool `json:"available,omitempty"`
	// Reason describes an unavailable output when applicable.
	Reason string `json:"reason,omitempty"`
}

// Lister lists available outputs.
type Lister interface {
	// List returns the outputs visible to the backend.
	List(ctx context.Context) ([]Info, error)
	// Close releases backend resources.
	Close() error
}

// OpenRuntime returns the best available output lister for rt.
func OpenRuntime(rt env.Runtime) (Lister, error) {
	display := rt.Display()
	sock := rt.SocketPath()
	if display != "" && sock == "" {
		return NewX11Lister(display)
	}
	if sock != "" {
		if wl.SocketReachable(sock) {
			return NewWaylandLister(sock)
		}
		if display != "" {
			return NewX11Lister(display)
		}
		return nil, fmt.Errorf("output: WAYLAND_DISPLAY=%q socket unreachable", sock)
	}
	return nil, fmt.Errorf("output: no display or Wayland socket available")
}

// ProbeRuntime reports how the output lister would be selected.
func ProbeRuntime(rt env.Runtime) []probe.Result {
	display := rt.Display()
	sock := rt.SocketPath()
	if display != "" && sock == "" {
		return []probe.Result{{Name: "x11", Available: true, Selected: true, Reason: "DISPLAY set"}}
	}
	if sock != "" {
		if !wl.SocketReachable(sock) {
			if display != "" {
				return []probe.Result{{Name: "x11", Available: true, Selected: true, Reason: "WAYLAND socket missing"}}
			}
			return []probe.Result{{Name: "wayland", Available: false, Reason: fmt.Sprintf("WAYLAND_DISPLAY=%q socket unreachable", sock)}}
		}
		return []probe.Result{{Name: "wayland", Available: true, Selected: true, Reason: compositor.DetectRuntime(rt).String()}}
	}
	return []probe.Result{{Name: "output", Available: false, Reason: "no output source available"}}
}
