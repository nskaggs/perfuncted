package cliname

import "testing"

func TestHyphenateCommandNaming(t *testing.T) {
	for _, tc := range []struct{ method, command string }{
		{"", ""},
		{"GrabFullHash", "grab-full-hash"},
		{"FindWindow", "find-window"},
		{"HTTPGet", "http-get"},
		{"URLValue", "url-value"},
		{"ID", "id"},
		{"List", "list"},
		{"ÉtagePlan", "étage-plan"},
	} {
		if got := Hyphenate(tc.method); got != tc.command {
			t.Errorf("Hyphenate(%q) = %q; want %q", tc.method, got, tc.command)
		}
	}
}
