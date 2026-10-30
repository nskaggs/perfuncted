//go:build linux

package input

import (
	"github.com/nskaggs/perfuncted/output"
	"strings"
	"testing"
)

// Reproduces the KDE report: perfuncted-touchpad: kernel bug: device has min == max on ABS_X.
// Validation must happen before opening any host device.
func TestUinputRejectsInvalidTouchpadDimensionsBeforeDeviceCreation(t *testing.T) {
	for _, bounds := range [][2]int32{{0, 0}, {0, 900}, {1600, 0}, {-1, 900}, {1600, -1}} {
		_, err := NewUinputBackend(bounds[0], bounds[1])
		if err == nil || !strings.Contains(err.Error(), "dimensions") {
			t.Errorf("bounds %v: wanted dimension error before touching /dev/uinput, got %v", bounds, err)
		}
	}
}

func TestUinputBoundsFromLogicalOutputs(t *testing.T) {
	tests := []struct {
		name         string
		outputs      []output.Info
		wantX, wantY int32
		wantErr      bool
	}{
		{name: "fractional scale uses logical output", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080, ScaleNumerator: 6, ScaleDenominator: 5}}, wantX: 1600, wantY: 900},
		{name: "dual displays", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}}, {Available: true, Geometry: output.Geometry{X: 1600, W: 1280, H: 1024}}}, wantX: 2880, wantY: 1024},
		{name: "negative offset fails closed", outputs: []output.Info{{Available: true, Geometry: output.Geometry{X: -100, W: 1600, H: 900}}}, wantErr: true},
		{name: "missing dimensions fail", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 0, H: 900}}}, wantErr: true},
		{name: "no outputs fail", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x, y, err := uinputBoundsFromOutputs(tt.outputs)
			if (err != nil) != tt.wantErr || (!tt.wantErr && (x != tt.wantX || y != tt.wantY)) {
				t.Fatalf("got (%d,%d) err=%v, want (%d,%d) err?%t", x, y, err, tt.wantX, tt.wantY, tt.wantErr)
			}
		})
	}
}
