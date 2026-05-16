package window

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"
)

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

type countingManager struct {
	fakeManager
	iterations int
}

func (m *countingManager) IterateWindows(ctx context.Context) iter.Seq2[Info, error] {
	m.iterations++
	return m.fakeManager.IterateWindows(ctx)
}

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

func TestWaitForMatchZeroPoll(t *testing.T) {
	m := &fakeManager{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := WaitForMatch(ctx, m, Match{TitleContains: "hello"}, 0)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestWaitForMatchPropagatesManagerError(t *testing.T) {
	want := errors.New("list failed")
	m := &errIterManager{err: want}

	_, err := WaitForMatch(context.Background(), m, Match{TitleContains: "hello"}, 0)
	if !errors.Is(err, want) {
		t.Fatalf("WaitForMatch error = %v, want %v", err, want)
	}
}

func TestWaitForMatchCanceledContextSkipsIteration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &countingManager{}

	_, err := WaitForMatch(ctx, m, Match{TitleContains: "hello"}, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForMatch error = %v, want context.Canceled", err)
	}
	if m.iterations != 0 {
		t.Fatalf("WaitForMatch iterated %d times after context cancellation, want 0", m.iterations)
	}
}

func TestWaitForMatchCloseZeroPoll(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "Hello World"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := WaitForMatchClose(ctx, m, Match{TitleContains: "hello"}, 0)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestWaitForMatchCloseCanceledContextSkipsIteration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &countingManager{fakeManager: fakeManager{wins: []Info{{ID: 1, Title: "Hello World"}}}}

	err := WaitForMatchClose(ctx, m, Match{TitleContains: "hello"}, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForMatchClose error = %v, want context.Canceled", err)
	}
	if m.iterations != 0 {
		t.Fatalf("WaitForMatchClose iterated %d times after context cancellation, want 0", m.iterations)
	}
}
