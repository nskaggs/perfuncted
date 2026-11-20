//go:build linux

package screen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A compositor-side replacement of the screenshot transport path must not
// turn the screenshot decoder into a reader of unrelated local files.
func TestGnomeScreenshotDecoderRejectsSymlinkOutsideTransportRoot(t *testing.T) {
	transportRoot := t.TempDir()
	unrelatedRoot := t.TempDir()
	secret := filepath.Join(unrelatedRoot, "private-document.png")
	if err := os.WriteFile(secret, []byte("not a screenshot"), 0600); err != nil {
		t.Fatal(err)
	}
	transport := filepath.Join(transportRoot, "transport.png")
	if err := os.Symlink(secret, transport); err != nil {
		t.Fatal(err)
	}
	img, err := decodeScreenshot(transport)
	if img != nil || err == nil || !strings.Contains(err.Error(), "path escapes") {
		t.Fatalf("decoder read outside transport root: img=%v error=%v", img, err)
	}
	b, err := os.ReadFile(secret)
	if err != nil || string(b) != "not a screenshot" {
		t.Fatalf("unrelated file changed: %q %v", b, err)
	}
}
