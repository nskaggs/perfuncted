package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/window"
)

type cancelingWindowReadBackend struct {
	*handleWindowManager
	cancel    context.CancelFunc
	listCalls int
	infoCalls int
}

func (b *cancelingWindowReadBackend) List(context.Context) ([]window.Info, error) {
	b.listCalls++
	b.cancel()
	return []window.Info{{NativeID: "private-1", Title: "private window"}}, nil
}
func (b *cancelingWindowReadBackend) InfoByID(_ context.Context, id string) (window.Info, error) {
	b.infoCalls++
	b.cancel()
	return window.Info{NativeID: id, Title: "new private title"}, nil
}

// A compositor can ignore caller cancellation and return metadata after its
// authorization has ended. That late result must not escape the Session.
func TestManagedWindowListRejectsMetadataFromCanceledRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &cancelingWindowReadBackend{handleWindowManager: newHandleWindowManager(), cancel: cancel}
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	got, err := session.Windows.List(ctx, WindowMatch{})
	if !errors.Is(err, context.Canceled) || len(got) != 0 {
		t.Fatalf("canceled window listing leaked %v, err=%v", got, err)
	}
	if backend.listCalls != 1 {
		t.Fatalf("unexpected calls: %d", backend.listCalls)
	}
}

// Window.Info must not commit newly observed metadata after cancellation: the
// cached snapshot is authoritative only for an accepted, live observation.
func TestManagedWindowInfoDoesNotPublishCanceledMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &cancelingWindowReadBackend{handleWindowManager: newHandleWindowManager(), cancel: cancel}
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	handle := session.Windows.window(window.Info{NativeID: "private-1", Title: "old title"})
	info, err := handle.Info(ctx)
	if !errors.Is(err, context.Canceled) || info != (window.Info{}) {
		t.Fatalf("canceled window metadata leaked %+v, err=%v", info, err)
	}
	if title := handle.Title(); title != "old title" {
		t.Fatalf("canceled window metadata overwritten cached title: %q", title)
	}
	if backend.infoCalls != 1 {
		t.Fatalf("unexpected calls: %d", backend.infoCalls)
	}
}

func TestManagedWindowHandlesDiscardResultsAfterSessionClose(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		backend := &cancelingWindowReadBackend{handleWindowManager: newHandleWindowManager()}
		session := NewSessionForTesting(nil, nil, backend, nil, nil)
		backend.cancel = func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}
		windows, err := session.Windows.List(context.Background(), WindowMatch{})
		if !errors.Is(err, ErrSessionClosed) || len(windows) != 0 {
			t.Fatalf("closed session leaked windows %+v, err=%v", windows, err)
		}
	})
	t.Run("info", func(t *testing.T) {
		backend := &cancelingWindowReadBackend{handleWindowManager: newHandleWindowManager()}
		session := NewSessionForTesting(nil, nil, backend, nil, nil)
		backend.cancel = func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}
		handle := session.Windows.window(window.Info{NativeID: "private-1", Title: "previous"})
		info, err := handle.Info(context.Background())
		if !errors.Is(err, ErrSessionClosed) || info != (window.Info{}) || handle.Title() != "previous" {
			t.Fatalf("closed session leaked window data %+v, title=%q err=%v", info, handle.Title(), err)
		}
	})
}
