package output

import (
	"context"
	"fmt"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
)

// Geometry describes an output rectangle in compositor coordinates.
type Geometry struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Info describes a read-only display output.
type Info struct {
	Name        string   `json:"name,omitempty"`
	Backend     string   `json:"backend"`
	Geometry    Geometry `json:"geometry"`
	ResolutionW int      `json:"resolution_w,omitempty"`
	ResolutionH int      `json:"resolution_h,omitempty"`
	Scale       int      `json:"scale,omitempty"`
	PhysicalW   int      `json:"physical_w,omitempty"`
	PhysicalH   int      `json:"physical_h,omitempty"`
	Make        string   `json:"make,omitempty"`
	Model       string   `json:"model,omitempty"`
	Description string   `json:"description,omitempty"`
	Primary     bool     `json:"primary,omitempty"`
	Available   bool     `json:"available,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// Lister lists available outputs.
type Lister interface {
	List(ctx context.Context) ([]Info, error)
	Close() error
}

// Open returns the best available output lister for the current environment.
func Open() (Lister, error) {
	return OpenRuntime(env.Current())
}

// OpenRuntime returns the best available output lister for rt.
func OpenRuntime(rt env.Runtime) (Lister, error) {
	if rt.Display() != "" && rt.SocketPath() == "" {
		return NewX11Lister(rt.Display())
	}
	if rt.SocketPath() != "" {
		return NewWaylandLister(rt.SocketPath())
	}
	return nil, fmt.Errorf("output: no display or Wayland socket available")
}

// ProbeRuntime reports how the output lister would be selected.
func ProbeRuntime(rt env.Runtime) []probe.Result {
	if rt.Display() != "" && rt.SocketPath() == "" {
		return []probe.Result{{Name: "x11", Available: true, Selected: true, Reason: "DISPLAY set"}}
	}
	if rt.SocketPath() != "" {
		return []probe.Result{{Name: "wayland", Available: true, Selected: true, Reason: compositor.DetectRuntime(rt).String()}}
	}
	return []probe.Result{{Name: "output", Available: false, Reason: "no output source available"}}
}

// List returns outputs from the current process environment.
func List(ctx context.Context) ([]Info, error) {
	l, err := Open()
	if err != nil {
		return nil, err
	}
	defer l.Close()
	return l.List(ctx)
}
