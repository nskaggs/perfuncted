package window

import "testing"

// The KWin script assigns position=i+1 while enumerating workspace.desktops.
// A response with gaps or mismatched ordering cannot be an authoritative
// rendering of that snapshot.
func TestKWinDesktopParserRejectsOutOfOrderAndNoncontiguousPositions(t *testing.T) {
	for _, tc := range []struct{ name, payload string }{
		{"skipped-position", `[{"id":"first","position":1,"current":true},{"id":"second","position":3}]`},
		{"swapped-position", `[{"id":"first","position":2,"current":true},{"id":"second","position":1}]`},
		{"start-at-two", `[{"id":"only","position":2,"current":true}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := parseKWinDesktops(tc.payload); err == nil {
				t.Fatalf("accepted KWin desktop positions inconsistent with enumeration: %+v", got)
			}
		})
	}
}
