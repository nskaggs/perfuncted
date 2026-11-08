package x11test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Displayfd is an untrusted server response. A partially parsed or negative
// number must not be exposed as a usable X11 DISPLAY identifier.
func TestServerRejectsMalformedDisplayFDIdentity(t *testing.T) {
	for _, tc := range []struct{ name, response string }{
		{"negative", "-1\\n"},
		{"trailing_letters", "217junk\\n"},
		{"leading_junk", "x217\\n"},
		{"empty", "\\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			server := filepath.Join(dir, "Xvfb")
			script := "#!/bin/sh\\nprintf '%s' '" + strings.TrimSuffix(tc.response, "\\n") + "' >&3\\nprintf '\\n' >&3\\nexec sleep 2\\n"
			// The script itself is generated only from fixed, local test cases.
			script = strings.ReplaceAll(script, "\\n", "\n")
			if err := os.WriteFile(server, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			display, stop, err := StartXvfb()
			if display != "" || stop != nil || err == nil {
				t.Fatalf("invalid displayfd %q accepted: display=%q stop=%v err=%v", tc.response, display, stop != nil, err)
			}
		})
	}
}
