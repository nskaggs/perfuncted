package input

import (
	"context"
	"testing"
)

func TestAuthorizedEISEmptyTypeDoesNotEmitFrames(t *testing.T) {
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx := context.Background()
	if err := backend.Type(ctx, ""); err != nil {
		t.Fatalf("empty key sequence: %v", err)
	}
	if err := backend.TypeLiteral(ctx, "probe"); err != nil {
		t.Fatalf("authorized follow-up text: %v", err)
	}
	assertSyntheticEISOperation(t, server, eiServerIDFloor+105, 2)
	if len(backend.heldKeys) != 0 {
		t.Fatalf("empty key sequence changed held keys: %v", backend.heldKeys)
	}
}
