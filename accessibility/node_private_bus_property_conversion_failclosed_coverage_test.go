package accessibility

import (
	"context"
	"strings"
	"testing"
)

// Real private D-Bus replies, rather than call overrides, exercise typed
// property decoding. Wrong provider types and unsupported destinations must
// preserve the caller's previous value instead of publishing partial data.
func TestPrivateATSPIPropertyConversionsFailClosedOnWrongTypes(t *testing.T) {
	b, id, provider := newOptionalATSPINode(t)
	provider.mu.Lock()
	provider.values[documentIface+".Locale"] = int32(12)
	provider.values[tableIface+".NRows"] = "twelve"
	provider.mu.Unlock()
	locale := "previous private value"
	if err := b.property(context.Background(), id, documentIface, "Locale", &locale); err == nil || !strings.Contains(err.Error(), "has type") || locale != "previous private value" {
		t.Fatalf("wrong-type locale replaced previous value: %q %v", locale, err)
	}
	rows := int32(77)
	if err := b.property(context.Background(), id, tableIface, "NRows", &rows); err == nil || !strings.Contains(err.Error(), "has type") || rows != 77 {
		t.Fatalf("wrong-type row count replaced previous value: %d %v", rows, err)
	}
	unsupported := true
	if err := b.property(context.Background(), id, documentIface, "Locale", &unsupported); err == nil || !strings.Contains(err.Error(), "unsupported property destination") || !unsupported {
		t.Fatalf("unsupported destination accepted or changed: %v %v", unsupported, err)
	}
}
