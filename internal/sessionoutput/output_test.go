package sessionoutput

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"testing"
)

// An absent output facade has no owner and cannot advertise capability
// support, even if the caller asks about an otherwise valid operation.
func TestNilOutputBundleCannotAdvertiseOrDispatch(t *testing.T) {
	var bundle *Owner
	if err := bundle.CheckAvailable("list"); !errors.Is(err, capability.ErrNilSession) {
		t.Fatalf("nil output facade advertised a list operation: %v", err)
	}
	if outputs, err := bundle.List(context.Background()); !errors.Is(err, capability.ErrNilSession) || outputs != nil {
		t.Fatalf("nil facade published outputs=%v err=%v", outputs, err)
	}
	if err := bundle.Close(); err != nil {
		t.Fatalf("closing nil facade was not idempotent: %v", err)
	}
}
