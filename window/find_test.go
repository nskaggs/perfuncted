package window

import "testing"

// fakeManager implements Manager for testing FindByTitle.
type fakeManager struct {
	wins []Info
}

func (f *fakeManager) List() ([]Info, error)         { return f.wins, nil }
func (f *fakeManager) Activate(string) error         { return nil }
func (f *fakeManager) Move(string, int, int) error   { return nil }
func (f *fakeManager) Resize(string, int, int) error { return nil }
func (f *fakeManager) ActiveTitle() (string, error)  { return "", nil }
func (f *fakeManager) CloseWindow(string) error      { return nil }
func (f *fakeManager) Minimize(string) error         { return nil }
func (f *fakeManager) Maximize(string) error         { return nil }
func (f *fakeManager) Close() error                  { return nil }

func TestFindByTitle_FindsMatch(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "Hello World"}, {ID: 2, Title: "Other"}}}
	w, err := FindByTitle(m, "world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.ID != 1 {
		t.Fatalf("expected ID 1, got %d", w.ID)
	}
}

func TestFindByTitle_NotFound(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "Foo"}}}
	_, err := FindByTitle(m, "bar")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if err.Error() != "window matching \"bar\" not found" {
		t.Fatalf("unexpected error message: %v", err)
	}
}
