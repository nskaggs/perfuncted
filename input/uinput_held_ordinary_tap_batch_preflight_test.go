//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// A tap releases its key; it cannot be allowed to consume an ordinary key
// that this same batch just placed into a persistent hold. Reject the whole
// command before emitting any prefix that could modify the focused target.
func TestUinputBatchRejectsTappingOrdinaryKeyHeldEarlierInCommand(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"tap after down", "{a down}{a}"},
		{"tap after safe prefix", "safe{enter down}{enter}"},
		{"named tap after separate hold", "{f5 down}{f5}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, kb := newTestBackend(t)
			err := backend.Type(context.Background(), tc.command)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("unsafe release-through-tap accepted: command=%q err=%v", tc.command, err)
			}
			if len(kb.events) != 0 {
				t.Fatalf("invalid batch injected prefix events: %v", kb.events)
			}
			if len(backend.heldKeys) != 0 {
				t.Fatalf("invalid batch retained phantom held keys: %v", backend.heldKeys)
			}
		})
	}
	backend, kb := newTestBackend(t)
	if err := backend.Type(context.Background(), "{a down}{a up}{a}"); err != nil {
		t.Fatalf("valid explicit release before tap rejected: %v", err)
	}
	if len(kb.events) != 3 || len(backend.heldKeys) != 0 {
		t.Fatalf("valid release-before-tap did not complete or leaked holds: events=%v holds=%v", kb.events, backend.heldKeys)
	}
}
