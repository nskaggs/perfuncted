package perfuncted

import (
	"context"
	"errors"
	"testing"
)

// An absent output facade has no owner and cannot advertise capability
// support, even if the caller asks about an otherwise valid operation.
func TestNilOutputBundleCannotAdvertiseOrDispatch(t *testing.T) {
	var bundle *OutputBundle
	if err := bundle.checkAvailable("list"); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil output facade advertised a list operation: %v", err)
	}
	if outputs, err := bundle.List(context.Background()); !errors.Is(err, ErrNilSession) || outputs != nil {
		t.Fatalf("nil facade published outputs=%v err=%v", outputs, err)
	}
	if err := bundle.close(); err != nil {
		t.Fatalf("closing nil facade was not idempotent: %v", err)
	}
}
