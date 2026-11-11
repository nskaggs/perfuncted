package window

import "testing"

// KWin desktop positions must be unique: a duplicated index makes ordered
// desktop state ambiguous even when stable IDs differ.
func TestKWinDesktopParserRejectsRepeatedPositions(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"same position", `[{"id":"desktop-a","position":1,"current":true},{"id":"desktop-b","position":1}]`},
		{"same later position", `[{"id":"desktop-a","position":1,"current":true},{"id":"desktop-b","position":2},{"id":"desktop-c","position":2}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseKWinDesktops(tc.data)
			if err == nil {
				t.Fatalf("accepted duplicate desktop positions: %+v", got)
			}
		})
	}
}
