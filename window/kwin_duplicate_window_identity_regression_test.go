//go:build linux

package window

import (
	"strings"
	"testing"
)

// KWin window handles must identify exactly one compositor window. A corrupt
// or stale scripting callback must not publish ambiguous title/process data.
func TestKWinWindowListRefusesDuplicateAndNonIdentifyingHandles(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"duplicate identical native id", `[{"id":"7","title":"Private A"},{"id":"7","title":"Private B"}]`},
		{"duplicate opaque native id", `[{"id":"{private-uuid}","title":"Private A"},{"id":"{private-uuid}","title":"Private B"}]`},
		{"blank native id", `[{"id":"   ","title":"Private"}]`},
		{"tab native id", `[{"id":"\t","title":"Private"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			windows, err := parseKWinWindowList(tc.body)
			if err == nil || len(windows) != 0 {
				t.Fatalf("unidentifiable window metadata accepted: %+v err=%v", windows, err)
			}
		})
	}
}

func TestKWinWindowListPreservesDistinctOpaqueAndNumericHandles(t *testing.T) {
	windows, err := parseKWinWindowList(`[{"id":"7","title":"Numeric"},{"id":"07","title":"Leading zero"},{"id":"{opaque}","title":"Opaque"}]`)
	if err != nil || len(windows) != 3 {
		t.Fatalf("distinct IDs rejected: %+v %v", windows, err)
	}
	for i, want := range []string{"7", "07", "{opaque}"} {
		if windows[i].StableID() != want || !strings.Contains(windows[i].Title, []string{"Numeric", "Leading zero", "Opaque"}[i]) {
			t.Fatalf("lost stable identity: %+v want %s", windows[i], want)
		}
	}
}
