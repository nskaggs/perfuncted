package window

import (
	"context"
	"errors"
	"iter"
	"testing"
)

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
