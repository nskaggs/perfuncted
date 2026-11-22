package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type cancelAtKeysendCheck struct {
	context.Context
	checks   int
	cancelAt int
}

func (c *cancelAtKeysendCheck) Err() error {
	c.checks++
	if c.checks >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestKeysendCoverageCancellationAtParserBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		cancelAt int
	}{
		{"between actions", "literal", 2},
		{"during closing brace scan", "{enter}", 3},
		{"during literal scan", "literal", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &cancelAtKeysendCheck{Context: context.Background(), cancelAt: tc.cancelAt}
			_, err := parseKeySendContext(ctx, tc.text)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected canceled parser, got %v after %d checks", err, ctx.checks)
			}
		})
	}
}

func TestKeysendCoverageChunkedIndexStopsAfterCancellation(t *testing.T) {
	ctx := &cancelAtKeysendCheck{Context: context.Background(), cancelAt: 2}
	index, err := indexByteContext(ctx, strings.Repeat("a", 32*1024+1), 'z')
	if index != -1 || !errors.Is(err, context.Canceled) || ctx.checks != 2 {
		t.Fatalf("index=%d err=%v checks=%d", index, err, ctx.checks)
	}
}

func TestKeysendCoverageKeyExpressionAndGrammarRejections(t *testing.T) {
	_, err := parseKeySendContext(context.Background(), "{"+strings.Repeat("a", maxKeyExpressionBytes+1)+"}")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize expression: %v", err)
	}
	if _, err := parseCombo("plain-key-without-modifier", false, false); err == nil {
		t.Fatal("accepted combo without separator")
	}
	if err := ValidateKeySequence(nil, "{enter}"); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := ValidateKeySequence(context.Background(), "{unfinished"); err == nil {
		t.Fatal("malformed sequence accepted")
	}
	if err := ValidateKeySequence(context.Background(), "{ctrl+s}literal"); err != nil {
		t.Fatalf("valid syntax: %v", err)
	}
	if got, err := parseBraced("ctrl down"); err != nil || !got.down || got.up || got.key != "ctrl" {
		t.Fatalf("explicit down parse: %+v, %v", got, err)
	}
}
