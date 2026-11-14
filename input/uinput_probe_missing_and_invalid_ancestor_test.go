package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Probing an unavailable /dev/uinput candidate must distinguish an absent
// device from a malformed path, and must not advertise permission or protocol
// support when no device has been successfully opened and identified.
func TestUinputProbeMissingDeviceAndInvalidParentFailClosed(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing-uinput")
	parent := filepath.Join(root, "ordinary-file")
	if err := os.WriteFile(parent, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path, reason string }{
		{"missing device", missing, "not found"},
		{"parent is regular file", filepath.Join(parent, "uinput"), "not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkUinputPath(tc.path)
			if got.Name != "uinput" || got.Available || !strings.Contains(strings.ToLower(got.Reason), tc.reason) {
				t.Fatalf("inaccessible path accepted or misdiagnosed: %+v", got)
			}
		})
	}
	data, err := os.ReadFile(parent)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("probe altered an unrelated parent: %q err=%v", data, err)
	}
}
