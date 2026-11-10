package screen

import (
	"testing"
)

// Portal Screenshot responds with a file URI, not a pathname. Unescaped query
// and fragment delimiters do not belong to its file path and must be rejected
// rather than accidentally interpreted as literal filesystem characters.
func TestPortalScreenshotURIRejectsUnescapedQueryAndFragment(t *testing.T) {
	for _, uri := range []string{
		"file:///var/tmp/capture.png?request=secret",
		"file:///var/tmp/capture.png#thumbnail",
		"file://localhost/var/tmp/capture.png?unexpected",
	} {
		if path, err := fileURIPath(uri); err == nil || path != "" {
			t.Errorf("ambiguous screenshot URI %q accepted as %q err=%v", uri, path, err)
		}
	}
	for _, tc := range []struct{ uri, path string }{
		{"file:///var/tmp/name%3Fpart.png", "/var/tmp/name?part.png"},
		{"file:///var/tmp/name%23part.png", "/var/tmp/name#part.png"},
	} {
		path, err := fileURIPath(tc.uri)
		if err != nil || path != tc.path {
			t.Errorf("escaped literal filename %q decoded path=%q err=%v", tc.uri, path, err)
		}
	}
}
