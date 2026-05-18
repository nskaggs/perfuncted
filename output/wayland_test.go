package output

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func wlStringData(s string) []byte {
	n := uint32(len(s) + 1)
	padded := (n + 3) &^ 3
	out := make([]byte, 4+int(padded))
	wl.PutUint32(out[0:4], n)
	copy(out[4:], s)
	return out
}

func appendWlStringData(dst []byte, s string) []byte {
	return append(dst, wlStringData(s)...)
}

func TestReadWlString(t *testing.T) {
	t.Parallel()

	data := wlStringData("HDMI-A-1")

	got, next, ok := readWlString(data, 0)
	if !ok {
		t.Fatal("readWlString returned ok=false")
	}
	if got != "HDMI-A-1" {
		t.Fatalf("string = %q, want %q", got, "HDMI-A-1")
	}
	if next != len(data) {
		t.Fatalf("next = %d, want %d", next, len(data))
	}
}

func TestReadWlStringRejectsMalformedData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "missing nul terminator",
			data: func() []byte {
				data := make([]byte, 8)
				wl.PutUint32(data[0:4], 4)
				copy(data[4:], "abcd")
				return data
			}(),
		},
		{
			name: "missing padding bytes",
			data: func() []byte {
				data := make([]byte, 6)
				wl.PutUint32(data[0:4], 2)
				data[4] = 'x'
				data[5] = 0
				return data
			}(),
		},
		{
			name: "length extends beyond data",
			data: func() []byte {
				data := make([]byte, 8)
				wl.PutUint32(data[0:4], 8)
				return data
			}(),
		},
		{
			name: "zero length",
			data: func() []byte {
				data := make([]byte, 4)
				wl.PutUint32(data[0:4], 0)
				return data
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, next, ok := readWlString(tt.data, 0); ok {
				t.Fatalf("readWlString(%v) = %q, next=%d, ok=true; want ok=false", tt.data, got, next)
			}
		})
	}
}

func TestWaylandOutputEventParsing(t *testing.T) {
	t.Parallel()

	out := &waylandOutput{
		info: Info{Name: "fallback-name", Backend: "wayland", Scale: 1},
	}
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)

	geometry := make([]byte, 20)
	wl.PutUint32(geometry[0:4], ^uint32(9))
	wl.PutUint32(geometry[4:8], 20)
	wl.PutUint32(geometry[8:12], 600)
	wl.PutUint32(geometry[12:16], 340)
	geometry = appendWlStringData(geometry, "Acme")
	geometry = appendWlStringData(geometry, "Panel 4K")
	proxy.OnEvent(0, 0, geometry)

	mode := make([]byte, 16)
	wl.PutUint32(mode[0:4], 1)
	wl.PutUint32(mode[4:8], 3840)
	wl.PutUint32(mode[8:12], 2160)
	proxy.OnEvent(1, 0, mode)

	scale := make([]byte, 4)
	wl.PutUint32(scale, 2)
	proxy.OnEvent(3, 0, scale)

	proxy.OnEvent(4, 0, wlStringData("DP-1"))
	proxy.OnEvent(5, 0, wlStringData("Acme Panel 4K"))

	if out.info.Geometry.X != -10 || out.info.Geometry.Y != 20 {
		t.Fatalf("geometry origin = %+v, want -10,20", out.info.Geometry)
	}
	if out.info.PhysicalW != 600 || out.info.PhysicalH != 340 {
		t.Fatalf("physical size = %dx%d, want 600x340", out.info.PhysicalW, out.info.PhysicalH)
	}
	if out.info.Make != "Acme" || out.info.Model != "Panel 4K" {
		t.Fatalf("make/model = %q/%q, want Acme/Panel 4K", out.info.Make, out.info.Model)
	}
	if out.info.ResolutionW != 3840 || out.info.ResolutionH != 2160 {
		t.Fatalf("resolution = %dx%d, want 3840x2160", out.info.ResolutionW, out.info.ResolutionH)
	}
	if out.info.Scale != 2 {
		t.Fatalf("scale = %d, want 2", out.info.Scale)
	}
	if out.info.Geometry.W != 1920 || out.info.Geometry.H != 1080 {
		t.Fatalf("geometry size = %dx%d, want 1920x1080", out.info.Geometry.W, out.info.Geometry.H)
	}
	if out.info.Name != "DP-1" {
		t.Fatalf("name = %q, want DP-1", out.info.Name)
	}
	if out.info.Description != "Acme Panel 4K" {
		t.Fatalf("description = %q, want Acme Panel 4K", out.info.Description)
	}
}

func TestWaylandOutputEventParsingIgnoresMalformedEvents(t *testing.T) {
	t.Parallel()

	out := &waylandOutput{
		info: Info{
			Name:        "before",
			Description: "description before",
			Geometry:    Geometry{X: 1, Y: 2, W: 3, H: 4},
			ResolutionW: 3,
			ResolutionH: 4,
			Scale:       1,
		},
	}
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)

	proxy.OnEvent(0, 0, []byte{1, 2, 3})
	proxy.OnEvent(1, 0, []byte{1, 2, 3})
	proxy.OnEvent(3, 0, []byte{1, 2, 3})
	proxy.OnEvent(4, 0, []byte{1, 2, 3})
	proxy.OnEvent(5, 0, []byte{1, 2, 3})

	if out.info.Name != "before" {
		t.Fatalf("name changed to %q after malformed event", out.info.Name)
	}
	if out.info.Description != "description before" {
		t.Fatalf("description changed to %q after malformed event", out.info.Description)
	}
	if out.info.Geometry != (Geometry{X: 1, Y: 2, W: 3, H: 4}) {
		t.Fatalf("geometry changed to %+v after malformed event", out.info.Geometry)
	}
}
