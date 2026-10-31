//go:build linux

package input

import (
	"context"
	"github.com/jezek/xgb/xproto"
	"strings"
	"testing"
)

// A rejected automation command must never type a valid prefix into the
// focused desktop before discovering a malformed or unreachable final key.
func TestXTestTypeRejectsUnreachableTailBeforeInjectingValidPrefix(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"named key unavailable", "a{f12}", "key"},
		{"literal Unicode unavailable", "aΩ", "character"},
		{"modifier key missing", "a{alt+a}", "key"},
		{"shifted key unsupported by layout", "aA", "character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
			err := b.Type(context.Background(), tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unreachable tail %q returned %v", tc.text, err)
			}
			if len(*events) != 0 {
				t.Fatalf("unreachable tail injected %d XTEST events: %+v", len(*events), *events)
			}
		})
	}
}
func TestXTestTypeLiteralRejectsUnreachableTailBeforeInjectingValidPrefix(t *testing.T) {
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
	err := b.TypeLiteral(context.Background(), "aΩ")
	if err == nil || !strings.Contains(err.Error(), "character") {
		t.Fatalf("unreachable literal accepted: %v", err)
	}
	if len(*events) != 0 {
		t.Fatalf("unreachable literal injected events: %+v", *events)
	}
}
