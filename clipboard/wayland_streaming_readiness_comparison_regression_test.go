package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A previous selection can be arbitrarily larger than the requested text.
// The readiness comparison must not buffer it in memory merely to reject it.
func TestWaylandReadinessStreamingComparisonBoundsUnrelatedSelections(t *testing.T) {
	const secret = "synthetic-private-selection-value"
	for _, tc := range []struct {
		name, output, expected string
		match                  bool
	}{
		{"matching", secret, secret, true},
		{"mismatching", "different", secret, false},
		{"extra bytes", secret + "-suffix", secret, false},
		{"truncated", secret[:10], secret, false},
		{"large unrelated", strings.Repeat("x", 4<<20), secret, false},
		{"empty", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			content := filepath.Join(dir, "selection")
			if err := os.WriteFile(content, []byte(tc.output), 0600); err != nil {
				t.Fatal(err)
			}
			backend := &extCmdClipboard{getCmd: []string{"cat", content}}
			if got := backend.waylandContentMatches(context.Background(), tc.expected); got != tc.match {
				t.Fatalf("comparison=%v, want %v", got, tc.match)
			}
			// The streaming comparator must retain only constant-size state,
			// independent of the prior clipboard selection length.
			comparator := &clipboardContentMatcher{want: tc.expected}
			for offset := 0; offset < len(tc.output); offset += 8192 {
				end := offset + 8192
				if end > len(tc.output) {
					end = len(tc.output)
				}
				if _, err := comparator.Write([]byte(tc.output[offset:end])); err != nil {
					t.Fatal(err)
				}
			}
			if got := comparator.matches(); got != tc.match {
				t.Fatalf("streaming match=%v want=%v", got, tc.match)
			}
		})
	}
}
