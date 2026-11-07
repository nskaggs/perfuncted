package window

import (
	"context"
	"errors"
	"testing"
)

// A canceled query cannot inspect invalid window identities or return
// metadata; its lifetime must be checked before parsing caller input.
func TestSwayInfoByIDRejectsCanceledCallerBeforeParsingWindowID(t *testing.T) {
	manager := &SwayManager{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := manager.InfoByID(ctx, "not-an-id"); !errors.Is(err, context.Canceled) || got != (Info{}) {
		t.Fatalf("canceled lookup returned info=%+v error=%v", got, err)
	}
}
