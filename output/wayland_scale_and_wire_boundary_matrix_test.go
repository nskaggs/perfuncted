package output

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWaylandOutputXDGLogicalGeometryEventMatrix(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		physicalW, physicalH                  uint32
		logicalW, logicalH                    int32
		wantN, wantD, wantScale, wantW, wantH int
	}{
		{"fractional five quarters", 2560, 1440, 2048, 1152, 5, 4, 0, 2048, 1152},
		{"fractional three halves", 1920, 1080, 1280, 720, 3, 2, 0, 1280, 720},
		{"integer double", 3840, 2160, 1920, 1080, 2, 1, 2, 1920, 1080},
		{"integer one", 1920, 1080, 1920, 1080, 1, 1, 1, 1920, 1080},
		{"incompatible aspect ratio", 1920, 1080, 1280, 700, 1, 1, 1, 1280, 700},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := newWaylandOutput(wl.GlobalEvent{Name: 14, Interface: "wl_output", Version: 4})
			phys, xdg := &wl.RawProxy{}, &wl.RawProxy{}
			out.updateProxy(phys)
			out.updateXDGProxy(xdg)
			mode := make([]byte, 16)
			wl.PutUint32(mode[:4], wl.OutputModeCurrent)
			wl.PutUint32(mode[4:8], tc.physicalW)
			wl.PutUint32(mode[8:12], tc.physicalH)
			phys.OnEvent(1, 0, mode)
			pos := make([]byte, 8)
			wl.PutUint32(pos[:4], uint32(100))
			wl.PutUint32(pos[4:8], uint32(200))
			xdg.OnEvent(0, 0, pos)
			sz := make([]byte, 8)
			wl.PutUint32(sz[:4], uint32(tc.logicalW))
			wl.PutUint32(sz[4:8], uint32(tc.logicalH))
			xdg.OnEvent(1, 0, sz)
			i := out.info
			if i.Geometry.X != 100 || i.Geometry.Y != 200 || i.Geometry.W != tc.wantW || i.Geometry.H != tc.wantH || i.ScaleNumerator != tc.wantN || i.ScaleDenominator != tc.wantD || i.Scale != tc.wantScale {
				t.Fatalf("xdg scale mismatch for %dx%d / %dx%d: %+v", tc.physicalW, tc.physicalH, tc.logicalW, tc.logicalH, i)
			}
			before := i
			for _, invalid := range [][]byte{nil, {1, 2, 3}, make([]byte, 8)} {
				if len(invalid) == 8 {
					wl.PutUint32(invalid[:4], 0)
					wl.PutUint32(invalid[4:8], 50)
				}
				xdg.OnEvent(1, 0, invalid)
				if out.info.Geometry != before.Geometry || out.info.Scale != before.Scale {
					t.Fatal("invalid XDG size corrupted prior geometry")
				}
			}
			phys.OnEvent(3, 0, []byte{0, 0, 0})
			if out.info.Scale != before.Scale {
				t.Fatal("invalid wl_output scale corrupted prior scale")
			}
		})
	}
}
func TestWaylandOutputRejectsMalformedXDGPositionAndPreservesPhysicalFallback(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 18, Interface: "wl_output", Version: 4})
	p, x := &wl.RawProxy{}, &wl.RawProxy{}
	out.updateProxy(p)
	out.updateXDGProxy(x)
	geometry := make([]byte, 20)
	wl.PutUint32(geometry[:4], uint32(5))
	wl.PutUint32(geometry[4:8], uint32(10))
	p.OnEvent(0, 0, geometry)
	mode := make([]byte, 16)
	wl.PutUint32(mode[:4], wl.OutputModeCurrent)
	wl.PutUint32(mode[4:8], 1920)
	wl.PutUint32(mode[8:12], 1080)
	p.OnEvent(1, 0, mode)
	scale := make([]byte, 4)
	wl.PutUint32(scale, 2)
	p.OnEvent(3, 0, scale)
	if got := out.info.Geometry; got.X != 5 || got.Y != 10 || got.W != 960 || got.H != 540 {
		t.Fatalf("fallback geometry not scaled: %+v", got)
	}
	for _, bad := range [][]byte{nil, {0, 0, 0, 0}} {
		x.OnEvent(0, 0, bad)
		if out.info.Geometry.X != 5 || out.info.Geometry.Y != 10 {
			t.Fatal("invalid logical position replaced physical coordinates")
		}
	}
	pos := make([]byte, 8)
	wl.PutUint32(pos[:4], uint32(45))
	wl.PutUint32(pos[4:8], uint32(30))
	x.OnEvent(0, 0, pos)
	if out.info.Geometry.X != 45 || out.info.Geometry.Y != 30 {
		t.Fatalf("logical position not used %+v", out.info)
	}
	x.OnEvent(3, 0, wlStringData("DP-test"))
	x.OnEvent(4, 0, wlStringData("Synthetic Display"))
	if out.info.Name != "DP-test" || out.info.Description != "Synthetic Display" {
		t.Fatalf("xdg name/description not propagated %+v", out.info)
	}
}
