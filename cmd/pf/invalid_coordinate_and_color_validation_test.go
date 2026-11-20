package main

import (
	"strings"
	"testing"
)

// CLI parsers must reject malformed coordinates and colors rather than
// silently accepting an unintended capture or cursor target.
func TestInvalidCoordinateAndColorTokensAreRejected(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		check func(string) error
		want  string
	}{
		{"rectangle nonnumeric coordinate", "0,2,not-an-int,4", func(s string) error { _, err := parseRect(s); return err }, "invalid number"},
		{"point nonnumeric y", "10,not-an-int", func(s string) error { _, err := parsePoint(s); return err }, "invalid y"},
		{"hex color invalid digit", "12X456", func(s string) error { _, err := parseColor(s); return err }, "invalid hex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.check(tc.input)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("input %q: got %v, want error containing %q", tc.input, err, tc.want)
			}
		})
	}
}
