//go:build linux

package screen

import (
	"strings"
	"testing"
)

func TestScreenshotPortalURIRejectsDecodedNULAndInvalidUTF8(t *testing.T) {
	for _, uri := range []string{
		"file:///tmp/capture%00secret.png",
		"file:///tmp/capture%FF.png",
		"file:///tmp/capture%C0%AF.png",
		"file:///tmp/capture%E2%82.png",
		"file:///tmp/" + string([]byte{0xfe}) + ".png",
	} {
		t.Run(uri, func(t *testing.T) {
			path, err := fileURIPath(uri)
			if err == nil || path != "" {
				t.Fatalf("untrusted portal screenshot URI accepted: path=%q err=%v", path, err)
			}
		})
	}
	for _, tc := range []struct{ uri, want string }{
		{"file:///tmp/capture%20safe.png", "/tmp/capture safe.png"},
		{"file://localhost/tmp/capture%2Bplus.png", "/tmp/capture+plus.png"},
	} {
		path, err := fileURIPath(tc.uri)
		if err != nil || !strings.EqualFold(path, tc.want) {
			t.Fatalf("valid portal URI incorrectly rejected: path=%q err=%v", path, err)
		}
	}
}
