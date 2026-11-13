package perfuncted_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type closeAfterFirstClickInput struct {
	pftest.Inputter
	after    func()
	releases int
}

func (b *closeAfterFirstClickInput) MouseUp(ctx context.Context, button int) error {
	b.releases++
	err := b.Inputter.MouseUp(ctx, button)
	if b.releases == 1 && b.after != nil {
		b.after()
	}
	return err
}

// A completed first click cannot authorize a second click once the session
// closes. The backend's Close may not cancel the caller context.
func TestDoubleClickDoesNotStartSecondClickAfterSessionCloses(t *testing.T) {
	backend := &closeAfterFirstClickInput{}
	session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	backend.after = func() { _ = session.Close() }
	err := session.Input.DoubleClick(context.Background(), 14, 23)
	want := []string{"move:14,23", "mousedown", "mouseup"}
	if !errors.Is(err, perfuncted.ErrSessionClosed) || !reflect.DeepEqual(backend.Calls, want) {
		t.Fatalf("double click after session closure: err=%v calls=%v want=%v", err, backend.Calls, want)
	}
}
