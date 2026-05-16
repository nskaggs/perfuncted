package window

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"testing"
	"time"
)

// errIterManager is a Manager whose IterateWindows yields a single error,
// simulating an I/O failure from the underlying window-list backend.
type errIterManager struct {
	err error
}

func (e *errIterManager) List(_ context.Context) ([]Info, error) { return nil, e.err }
func (e *errIterManager) IterateWindows(_ context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		yield(Info{}, e.err)
	}
}
func (e *errIterManager) Activate(_ context.Context, _ string) error         { return nil }
func (e *errIterManager) Move(_ context.Context, _ string, _, _ int) error   { return nil }
func (e *errIterManager) Resize(_ context.Context, _ string, _, _ int) error { return nil }
func (e *errIterManager) ActiveTitle(_ context.Context) (string, error)      { return "", nil }
func (e *errIterManager) CloseWindow(_ context.Context, _ string) error      { return nil }
func (e *errIterManager) Minimize(_ context.Context, _ string) error         { return nil }
func (e *errIterManager) Maximize(_ context.Context, _ string) error         { return nil }
func (e *errIterManager) Fullscreen(_ context.Context, _ string) error       { return nil }
func (e *errIterManager) Unfullscreen(_ context.Context, _ string) error     { return nil }
func (e *errIterManager) Restore(_ context.Context, _ string) error          { return nil }
func (e *errIterManager) Close() error                                       { return nil }

// TestWaitForMatchClose_PropagatesIOError verifies that WaitForMatchClose does
// not return nil when Find returns an error that is NOT ErrWindowNotFound.
//
// Prior to the fix, WaitForMatchClose returned nil for any error from Find,
// silently swallowing I/O errors and context-cancellation errors and
// incorrectly signalling that the window had closed.
func TestWaitForMatchClose_PropagatesIOError(t *testing.T) {
	ioErr := fmt.Errorf("backend: connection lost")
	m := &errIterManager{err: ioErr}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := WaitForMatchClose(ctx, m, Match{TitleContains: "anything"}, 10*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForMatchClose: got nil but expected the underlying I/O error to be propagated")
	}
	if !errors.Is(err, ioErr) {
		t.Fatalf("WaitForMatchClose: got %v, want error wrapping %v", err, ioErr)
	}
}

// TestWaitForClose_PropagatesIOError is the same as above for the
// pattern-string convenience wrapper WaitForClose.
func TestWaitForClose_PropagatesIOError(t *testing.T) {
	ioErr := fmt.Errorf("backend: socket closed")
	m := &errIterManager{err: ioErr}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := WaitForClose(ctx, m, "anything", 10*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForClose: got nil but expected the underlying I/O error to be propagated")
	}
	if !errors.Is(err, ioErr) {
		t.Fatalf("WaitForClose: got %v, want error wrapping %v", err, ioErr)
	}
}

// TestWaitForMatchClose_SucceedsOnErrWindowNotFound verifies that the
// ErrWindowNotFound path still works correctly after the fix: the function
// must return nil when the window genuinely disappears.
func TestWaitForMatchClose_SucceedsOnErrWindowNotFound(t *testing.T) {
	// No windows → Find returns ErrWindowNotFound immediately.
	m := &fakeManager{wins: nil}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := WaitForMatchClose(ctx, m, Match{TitleContains: "anything"}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForMatchClose: got %v, want nil when window is not found", err)
	}
}
