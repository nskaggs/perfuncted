//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// A malformed or unmappable tail must never leave a prefix typed into a
// focused application. Syntactic parsing alone cannot establish that every
// token can be translated to the current kernel keyboard layout.
func TestUinputRejectsUnmappableKeySequencesBeforeSendingAnyInput(t *testing.T) {
	for _, tc := range []struct {
		name, sequence string
		literal        bool
	}{
		{"unsupported key after valid text", "some text{not-a-real-key}", false},
		{"unsupported character after valid text", "abc€", false},
		{"unsupported key after valid combination", "{ctrl+a}{not-a-real-key}", false},
		{"unsupported literal after valid text", "valid€", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, kb := newTestBackend(t)
			var err error
			if tc.literal {
				err = b.TypeLiteral(context.Background(), tc.sequence)
			} else {
				err = b.Type(context.Background(), tc.sequence)
			}
			if err == nil {
				t.Fatal("unmappable sequence accepted")
			}
			if len(kb.events) != 0 {
				t.Fatalf("unmappable sequence injected partial keyboard events: %v", kb.events)
			}
		})
	}
}

func TestUinputRejectsMalformedSyntaxWithoutInjectingInput(t *testing.T) {
	for _, sequence := range []string{"before{ctrl+}", "before{alt+ctrl}", "before{unclosed"} {
		b, kb := newTestBackend(t)
		if err := b.Type(context.Background(), sequence); err == nil {
			t.Fatalf("malformed syntax accepted: %q", sequence)
		}
		if len(kb.events) != 0 {
			t.Fatalf("malformed syntax typed before failing %q: %v", sequence, kb.events)
		}
	}
}

func TestUinputPreflightStillAcceptsValidMultistepCommands(t *testing.T) {
	b, kb := newTestBackend(t)
	sequence := "Ab{ctrl+s}cd"
	if err := b.Type(context.Background(), sequence); err != nil {
		t.Fatalf("valid sequence rejected: %v", err)
	}
	if len(kb.events) == 0 || !strings.Contains(strings.Join(kb.events, ";"), "press:") {
		t.Fatalf("valid sequence never reached keyboard: %v", kb.events)
	}
}
