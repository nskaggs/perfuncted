package clipboard

import (
	"context"
	"strings"
	"testing"
)

// A partially initialized command backend must return an error rather than
// panic when a session attempts to read or set a selection.
func TestClipboardIncompleteCommandBackendFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		clip *extCmdClipboard
	}{
		{name: "nil backend", clip: nil},
		{name: "zero value", clip: &extCmdClipboard{}},
		{name: "empty read executable", clip: &extCmdClipboard{getCmd: []string{""}, setCmd: []string{"/bin/true"}}},
		{name: "empty write executable", clip: &extCmdClipboard{getCmd: []string{"/bin/true"}, setCmd: []string{""}}},
		{name: "only read command", clip: &extCmdClipboard{getCmd: []string{"/bin/true"}}},
		{name: "only write command", clip: &extCmdClipboard{setCmd: []string{"/bin/true"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clip == nil || len(tc.clip.getCmd) == 0 || tc.clip.getCmd[0] == "" {
				if got, err := tc.clip.Get(context.Background()); got != "" || err == nil || !strings.Contains(err.Error(), "not configured") {
					t.Fatalf("Get on incomplete backend: text=%q error=%v", got, err)
				}
			}
			if tc.clip == nil || len(tc.clip.setCmd) == 0 || tc.clip.setCmd[0] == "" {
				if err := tc.clip.Set(context.Background(), "sensitive clipboard value"); err == nil || !strings.Contains(err.Error(), "not configured") {
					t.Fatalf("Set on incomplete backend: %v", err)
				}
			}
		})
	}
}
