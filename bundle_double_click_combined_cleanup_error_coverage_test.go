package perfuncted_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type doubleClickDualFailureBackend struct {
	pftest.Inputter
	pressError   error
	releaseError error
}

func (b *doubleClickDualFailureBackend) MouseDown(ctx context.Context, button int) error {
	_ = b.Inputter.MouseDown(ctx, button)
	return b.pressError
}
func (b *doubleClickDualFailureBackend) MouseUp(ctx context.Context, button int) error {
	_ = b.Inputter.MouseUp(ctx, button)
	return b.releaseError
}

// A failed press still requires a cleanup release, and neither failure may
// hide the other in the receipt returned to the caller.
func TestDoubleClickJoinsPressAndCleanupReleaseErrors(t *testing.T) {
	pressError := errors.New("synthetic click press failed")
	releaseError := errors.New("synthetic click release cleanup failed")
	backend := &doubleClickDualFailureBackend{pressError: pressError, releaseError: releaseError}
	session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	err := session.Input.DoubleClick(context.Background(), 5, 7)
	expected := []string{"move:5,7", "mousedown", "mouseup"}
	if !errors.Is(err, pressError) || !errors.Is(err, releaseError) || !reflect.DeepEqual(backend.Calls, expected) {
		t.Fatalf("double-click error=%v calls=%v, expected both errors and %v", err, backend.Calls, expected)
	}
}
