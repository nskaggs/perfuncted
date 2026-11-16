package window

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Window lookups must not return stale matches when their owner has already
// canceled the request, even if a backend is otherwise available.
func TestFindByIDPreCanceledRequestDoesNotEnumerateWindows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := FindByID(ctx, &fakeManager{wins: []Info{{ID: 42}}}, 42)
	if !errors.Is(err, context.Canceled) || got.ID != 0 {
		t.Fatalf("canceled FindByID = (%+v, %v), want no window and canceled", got, err)
	}
}

// A stream I/O error is not equivalent to an empty window listing.
func TestFindByTitleReturnsBackendIterationError(t *testing.T) {
	backendErr := errors.New("window enumeration transport disconnected")
	got, err := FindByTitle(context.Background(), &errIterManager{err: backendErr}, "editor")
	if !errors.Is(err, backendErr) || got.ID != 0 {
		t.Fatalf("failing FindByTitle = (%+v, %v), want backend error", got, err)
	}
}

// A missing backend and an already-canceled wait are distinct terminal cases:
// neither is success just because the window cannot be observed.
func TestWaitForMatchCloseMissingBackendAndCanceledWait(t *testing.T) {
	if err := WaitForMatchClose(context.Background(), nil, Match{TitleContains: "editor"}, time.Millisecond); err == nil {
		t.Fatal("nil backend incorrectly reported window closed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitForMatchClose(ctx, &fakeManager{}, Match{TitleContains: "editor"}, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait reported window closed: %v", err)
	}
}

// Cancellation occurring inside an empty iterator must take precedence over
// a successful close observation, even when no window was yielded.
func TestWaitForMatchCloseRejectsCancellationAfterEmptyIteration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &cancellingIteratorManager{fakeManager: &fakeManager{}, cancel: cancel}
	err := WaitForMatchClose(ctx, manager, Match{TitleContains: "editor"}, time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation during enumeration was treated as close: %v", err)
	}
}
