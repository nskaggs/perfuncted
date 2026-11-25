package window

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"
	"time"
)

func BenchmarkMatchMatches_TitleContains(b *testing.B) {
	info := Info{ID: 1, Title: "Firefox Web Browser", AppID: "org.mozilla.firefox", Active: true}
	m := Match{TitleContains: "firefox"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.Matches(info)
	}
}

func BenchmarkMatcherMatches_TitleContains(b *testing.B) {
	info := Info{ID: 1, Title: "Firefox Web Browser", AppID: "org.mozilla.firefox", Active: true}
	matcher := CompileMatch(Match{TitleContains: "firefox"})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = matcher.Matches(info)
	}
}

func BenchmarkMatchMatches_TitleExact(b *testing.B) {
	info := Info{ID: 1, Title: "Firefox Web Browser", AppID: "org.mozilla.firefox"}
	m := Match{TitleExact: "firefox web browser"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.Matches(info)
	}
}

func BenchmarkMatchMatches_MultiField(b *testing.B) {
	active := true
	info := Info{ID: 7, Title: "Editor", AppID: "org.example.editor", PID: 42, Active: true}
	m := Match{TitleContains: "editor", AppID: "org.example.editor", Active: &active}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.Matches(info)
	}
}

func BenchmarkFindByTitle(b *testing.B) {
	wins := make([]Info, 20)
	for i := range wins {
		wins[i] = Info{ID: uint64(i + 1), Title: "Window " + string(rune('A'+i)), AppID: "org.example.app"}
	}
	wins[19].Title = "Target Window"
	m := &fakeManager{wins: wins}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = FindByTitle(context.Background(), m, "target")
	}
}

func BenchmarkFind_MatchByAppID(b *testing.B) {
	wins := make([]Info, 20)
	for i := range wins {
		wins[i] = Info{ID: uint64(i + 1), Title: "Window", AppID: "org.other.app"}
	}
	wins[19].AppID = "org.target.app"
	m := &fakeManager{wins: wins}
	match := Match{AppID: "org.target.app"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = FindByTitle(context.Background(), m, match.TitleContains)
	}
}

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

type cancellingIteratorManager struct {
	*fakeManager
	cancel  context.CancelFunc
	windows []Info
}

func (m *cancellingIteratorManager) IterateWindows(context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		m.cancel()
		for _, win := range m.windows {
			if !yield(win, nil) {
				return
			}
		}
	}
}
func TestFindByTitleDoesNotAcceptMatchAfterIteratorCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := &cancellingIteratorManager{fakeManager: &fakeManager{}, cancel: cancel, windows: []Info{{ID: 1, Title: "Firefox"}}}
	_, err := FindByTitle(ctx, mgr, "Firefox")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled iterator found window: %v", err)
	}
}
func TestFindByTitleDoesNotReportNotFoundAfterIteratorCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := &cancellingIteratorManager{fakeManager: &fakeManager{}, cancel: cancel}
	_, err := FindByTitle(ctx, mgr, "Firefox")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled iterator reported notfound: %v", err)
	}
}

// ── clampPoll ─────────────────────────────────────────────────────────────────

func TestClampPoll_Zero(t *testing.T) {
	got := clampPoll(0)
	if got != 10*time.Millisecond {
		t.Fatalf("clampPoll(0) = %v, want 10ms", got)
	}
}

func TestClampPoll_Negative(t *testing.T) {
	got := clampPoll(-5 * time.Millisecond)
	if got != 10*time.Millisecond {
		t.Fatalf("clampPoll(-5ms) = %v, want 10ms", got)
	}
}

func TestClampPoll_Positive(t *testing.T) {
	got := clampPoll(20 * time.Millisecond)
	if got != 20*time.Millisecond {
		t.Fatalf("clampPoll(20ms) = %v, want 20ms", got)
	}
}

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

// fakeManager implements Manager for testing FindByTitle.
type fakeManager struct {
	wins []Info
}

func (f *fakeManager) List(ctx context.Context) ([]Info, error) {
	return f.wins, nil
}

func (f *fakeManager) IterateWindows(ctx context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		for _, w := range f.wins {
			if !yield(w, nil) {
				return
			}
		}
	}
}

func (f *fakeManager) Activate(ctx context.Context, _ string) error         { return nil }
func (f *fakeManager) Move(ctx context.Context, _ string, _, _ int) error   { return nil }
func (f *fakeManager) Resize(ctx context.Context, _ string, _, _ int) error { return nil }
func (f *fakeManager) ActiveTitle(ctx context.Context) (string, error)      { return "", nil }
func (f *fakeManager) CloseWindow(ctx context.Context, _ string) error      { return nil }
func (f *fakeManager) Minimize(ctx context.Context, _ string) error         { return nil }
func (f *fakeManager) Maximize(ctx context.Context, _ string) error         { return nil }
func (f *fakeManager) Fullscreen(ctx context.Context, _ string) error       { return nil }
func (f *fakeManager) Unfullscreen(ctx context.Context, _ string) error     { return nil }
func (f *fakeManager) Restore(ctx context.Context, _ string) error          { return nil }
func (f *fakeManager) Close() error                                         { return nil }
func TestFindByTitle_FindsMatch(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "Hello World"}, {ID: 2, Title: "Other"}}}
	w, err := FindByTitle(context.Background(), m, "world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.ID != 1 {
		t.Fatalf("expected ID 1, got %d", w.ID)
	}
}

func TestFindByTitle_NotFound(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "Foo"}}}
	_, err := FindByTitle(context.Background(), m, "bar")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "window: not found") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestFindByTitleNilManager(t *testing.T) {
	var m Manager = (*fakeManager)(nil)

	_, err := FindByTitle(context.Background(), m, "hello")
	if err == nil {
		t.Fatal("expected error for nil manager")
	}
}
