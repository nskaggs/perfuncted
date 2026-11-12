package window

import (
	"math"
	"strings"
	"testing"
)

// KWin JSON cannot express NaN or infinity directly, but a decoded or
// adapter-supplied row can. Never turn such compositor geometry into a
// plausible screen coordinate or disclose a partially parsed window.
func TestKWinWindowRowRejectsNonFiniteCoordinatesWithFieldContext(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  float64
		change func(*kwinWindowRow, float64)
	}{
		{"x nan", math.NaN(), func(r *kwinWindowRow, v float64) { r.X = v }},
		{"y negative infinity", math.Inf(-1), func(r *kwinWindowRow, v float64) { r.Y = v }},
		{"width positive infinity", math.Inf(1), func(r *kwinWindowRow, v float64) { r.Width = v }},
		{"height nan", math.NaN(), func(r *kwinWindowRow, v float64) { r.Height = v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := kwinWindowRow{ID: "private-window-27", X: 12, Y: 14, Width: 640, Height: 480}
			tc.change(&row, tc.value)
			got, err := parseKWinWindowRow(row)
			field := strings.Split(tc.name, " ")[0]
			if err == nil || got != (Info{}) || !strings.Contains(err.Error(), row.ID) || !strings.Contains(err.Error(), "geometry "+field+" is not finite") {
				t.Fatalf("invalid KWin coordinate accepted: row=%+v window=%+v err=%v", row, got, err)
			}
		})
	}
}

func TestKWinWindowRowRejectsOutOfRangeOffsetsAndDimensions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*kwinWindowRow)
	}{
		{"x overflow", func(r *kwinWindowRow) { r.X = math.MaxFloat64 }},
		{"y underflow", func(r *kwinWindowRow) { r.Y = -math.MaxFloat64 }},
		{"width overflow", func(r *kwinWindowRow) { r.Width = math.MaxFloat64 }},
		{"height underflow", func(r *kwinWindowRow) { r.Height = -math.MaxFloat64 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := kwinWindowRow{ID: "private-window-27", X: 12, Y: 14, Width: 640, Height: 480}
			tc.change(&row)
			got, err := parseKWinWindowRow(row)
			if err == nil || got != (Info{}) || !strings.Contains(err.Error(), row.ID) || !strings.Contains(err.Error(), "exceeds int range") {
				t.Fatalf("out-of-range KWin geometry accepted: row=%+v window=%+v err=%v", row, got, err)
			}
		})
	}
}
