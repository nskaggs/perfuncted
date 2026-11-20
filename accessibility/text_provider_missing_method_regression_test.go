package accessibility

import (
	"context"
	"strings"
	"testing"
)

func TestTextReadReportsMissingProviderMethodWithDefaultLimit(t *testing.T) {
	backend, id, provider := newOptionalATSPINode(t)
	provider.mu.Lock()
	provider.values[textIface+".CharacterCount"] = int32(7)
	provider.mu.Unlock()
	node := Node{}
	err := backend.readNodeText(context.Background(), id, []string{textIface}, 0, &node)
	if err == nil || !strings.Contains(err.Error(), textIface) || node.Text != "" {
		t.Fatalf("missing text method returned text=%q err=%v", node.Text, err)
	}
}
