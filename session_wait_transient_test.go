package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

// flakyWindowManager fails the first N List calls, then reports windows.
type flakyWindowManager struct {
	mu        sync.Mutex
	failures  int
	err       error
	windows   []window.Info
	listCalls int
}

func (m *flakyWindowManager) List(context.Context) ([]window.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listCalls++
	if m.listCalls <= m.failures {
		return nil, m.err
	}
	return append([]window.Info(nil), m.windows...), nil
}

func (m *flakyWindowManager) IterateWindows(
	ctx context.Context,
) iter.Seq2[window.Info, error] {
	return func(yield func(window.Info, error) bool) {
		windows, err := m.List(ctx)
		if err != nil {
			yield(window.Info{}, err)
			return
		}
		for _, w := range windows {
			if !yield(w, nil) {
				return
			}
		}
	}
}

func (m *flakyWindowManager) ActiveTitle(context.Context) (string, error) {
	return "", nil
}

func (m *flakyWindowManager) Close() error { return nil }

func TestSessionWaitRetriesTransientEvaluationErrors(t *testing.T) {
	manager := &flakyWindowManager{
		failures: 5,
		err:      fmt.Errorf("window/sway: get_tree: read unix: i/o timeout"),
		windows:  []window.Info{{NativeID: "ready", Title: "Ready"}},
	}
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := session.Wait(ctx, WindowExists(WindowMatch{TitleExact: "Ready"}), WaitEvery(5*time.Millisecond)); err != nil {
		t.Fatalf("Wait aborted on transient evaluation errors: %v", err)
	}
}

func TestSessionWaitAbortsOnPermanentEvaluationError(t *testing.T) {
	manager := &flakyWindowManager{
		failures: 1000,
		err:      ErrApplicationExited,
	}
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := session.Wait(ctx, WindowExists(WindowMatch{TitleExact: "Ready"}), WaitEvery(5*time.Millisecond))
	if err == nil || !errors.Is(err, ErrApplicationExited) {
		t.Fatalf("Wait error = %v; want immediate ErrApplicationExited", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("permanent error took %v to abort; want fast failure", elapsed)
	}
}

func TestSessionWaitGivesUpAfterSustainedTransientErrors(t *testing.T) {
	manager := &flakyWindowManager{
		failures: 1000,
		err:      fmt.Errorf("window/sway: get_tree: read unix: i/o timeout"),
	}
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := session.Wait(ctx, WindowExists(WindowMatch{TitleExact: "Ready"}), WaitEvery(2*time.Millisecond))
	if err == nil || !strings.Contains(err.Error(), "consecutive evaluation errors") {
		t.Fatalf("Wait error = %v; want sustained-failure abort", err)
	}
}
