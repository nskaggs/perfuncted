package accessibility

import (
	"context"
	"strings"
	"testing"
)

// When AT-SPI reports more characters than the client's read budget, that
// prefix is incomplete evidence. A matching text selector must not treat an
// omitted suffix as a verified negative result.
func TestTextProviderCharacterLimitMarksSemanticEvidenceTruncated(t *testing.T) {
	const original = "start of document with a hidden important suffix"
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) {
		frame.text = original
	})
	node, err := backend.readNodeFresh(context.Background(), root, NodeID{}, 8, true)
	if err != nil {
		t.Fatalf("read node: %v", err)
	}
	if node.Text != original[:8] || !node.TextTruncated {
		t.Fatalf("provider text prefix=%q truncated=%t, want bounded incomplete evidence", node.Text, node.TextTruncated)
	}
	snap := Snapshot{Nodes: []Node{node}, Root: node, Generation: root.Generation}
	if err := ValidateSemanticSnapshot(snap, Selector{Text: "hidden important suffix"}); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("text selector silently accepted truncated evidence: %v", err)
	}
}

// Ordinary complete text must not be labeled incomplete merely because it
// exactly matches the configured read limit; empty providers are complete too.
func TestTextProviderCompleteCharacterCountRemainsAuthoritative(t *testing.T) {
	for _, tc := range []struct {
		name, text    string
		limit         int
		wantTruncated bool
	}{
		{name: "text exactly at budget", text: "eight123", limit: 8},
		{name: "short text", text: "okay", limit: 8},
		{name: "empty text", text: "", limit: 8},
		{name: "prefix exceeds budget", text: "longer than max", limit: 5, wantTruncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) { frame.text = tc.text })
			node, err := backend.readNodeFresh(context.Background(), root, NodeID{}, tc.limit, true)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			expected := tc.text
			if len(expected) > tc.limit {
				expected = expected[:tc.limit]
			}
			if node.Text != expected || node.TextTruncated != tc.wantTruncated {
				t.Fatalf("got text=%q truncated=%t, want %q %t", node.Text, node.TextTruncated, expected, tc.wantTruncated)
			}
		})
	}
}
