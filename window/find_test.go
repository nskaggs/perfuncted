package window

import (
	"context"
	"testing"
)

// fakeManager implements Manager for testing FindByTitle.
type fakeManager struct {
	wins []Info
}

func (f *fakeManager) List(ctx context.Context) ([]Info, error)             { return f.wins, nil }
func (f *fakeManager) Activate(ctx context.Context, _ string) error         { return nil }
func (f *fakeManager) Move(ctx context.Context, _ string, _, _ int) error   { return nil }
func (f *fakeManager) Resize(ctx context.Context, _ string, _, _ int) error { return nil }
func (f *fakeManager) ActiveTitle(ctx context.Context) (string, error)      { return "", nil }
func (f *fakeManager) CloseWindow(ctx context.Context, _ string) error      { return nil }
func (f *fakeManager) Minimize(ctx context.Context, _ string) error         { return nil }
func (f *fakeManager) Maximize(ctx context.Context, _ string) error         { return nil }
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
	if err.Error() != "window matching \"bar\" not found" {
		t.Fatalf("unexpected error message: %v", err)
	}
}
