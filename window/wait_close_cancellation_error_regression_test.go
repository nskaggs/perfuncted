package window

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"
)

type canceledErrorWindowIterator struct {
	*fakeManager
	cancel  context.CancelFunc
	emitted int
}

func (m *canceledErrorWindowIterator) IterateWindows(context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		m.cancel()
		m.emitted++
		yield(Info{}, errors.New("window backend disconnected after caller cancellation"))
	}
}

func TestWaitForMatchClosePrefersCancellationAfterIteratorError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &canceledErrorWindowIterator{fakeManager: &fakeManager{}, cancel: cancel}
	err := WaitForMatchClose(ctx, m, Match{TitleContains: "Firefox"}, time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("returned backend error instead of caller cancellation: %v", err)
	}
	if m.emitted != 1 {
		t.Fatalf("iterated %d times, expected one", m.emitted)
	}
}

func TestWaitForMatchClosePreservesBackendErrorWithoutCancellation(t *testing.T) {
	cause := errors.New("real iterator error")
	m := &windowErrorIterator{fakeManager: &fakeManager{}, cause: cause}
	err := WaitForMatchClose(context.Background(), m, Match{TitleContains: "Firefox"}, time.Millisecond)
	if !errors.Is(err, cause) {
		t.Fatalf("backend failure=%v, want %v", err, cause)
	}
}

type windowErrorIterator struct {
	*fakeManager
	cause error
}

func (m *windowErrorIterator) IterateWindows(context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) { yield(Info{}, m.cause) }
}
