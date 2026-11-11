package window

import "testing"

// KWin's script explicitly serializes arrays, including [] when none exist.
// JSON null has no corresponding successful compositor list response and must
// not be silently treated as an empty desktop or window inventory.
func TestKWinListParsersRejectNullCompositorPayloads(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parse func(string) error
	}{
		{"desktops", func(data string) error { _, err := parseKWinDesktops(data); return err }},
		{"windows", func(data string) error { _, err := parseKWinWindowList(data); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.parse("null"); err == nil {
				t.Fatal("accepted null KWin response as an authoritative empty list")
			}
		})
	}
}
