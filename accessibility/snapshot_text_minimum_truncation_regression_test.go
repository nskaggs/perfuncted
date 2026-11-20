package accessibility

import (
	"testing"
	"unicode/utf8"
)

// A one-byte or multibyte character must still make progress when snapshot
// bounding halves nonempty text. No invalid UTF-8 or private original text
// may escape when the remaining response budget admits only the empty node.
func TestSnapshotNodeBudgetDropsShortestTextWithoutInvalidUTF8(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{name: "single byte", value: "x"},
		{name: "multibyte rune", value: "é"},
		{name: "astral rune", value: "😀"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := Node{Text: tc.value}
			budget := estimateNodeJSONSize(Node{}) + 60
			if estimateNodeJSONSize(original)+60 <= budget {
				t.Fatalf("test did not require truncation: value=%q budget=%d", tc.value, budget)
			}
			bounded := boundNodeResponse(original, budget)
			if bounded.Text != "" || !bounded.TextTruncated || !utf8.ValidString(bounded.Text) {
				t.Fatalf("short text escaped response budget: original=%q bounded=%+v", tc.value, bounded)
			}
			if original.Text != tc.value || original.TextTruncated {
				t.Fatalf("bounding mutated input node: %+v", original)
			}
			// The minimum node including its TextTruncated marker may still
			// exceed this intentionally impossible budget. The caller's
			// minimum-response fallback handles that case separately.
		})
	}
}
