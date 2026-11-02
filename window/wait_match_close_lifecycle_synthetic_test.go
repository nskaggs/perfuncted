package window

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"
)

type sequencedCloseWindowManager struct {
	*fakeManager
	calls    int
	sequence [][]Info
}

func (m *sequencedCloseWindowManager) IterateWindows(context.Context) iter.Seq2[Info, error] {
	m.calls++
	i := m.calls - 1
	if i >= len(m.sequence) {
		i = len(m.sequence) - 1
	}
	return func(yield func(Info, error) bool) {
		for _, w := range m.sequence[i] {
			if !yield(w, nil) {
				return
			}
		}
	}
}

func TestWaitForMatchClosePollsUntilTargetActuallyDisappears(t *testing.T) {
	m := &sequencedCloseWindowManager{fakeManager: &fakeManager{}, sequence: [][]Info{
		{{ID: 51, Title: "target app"}, {ID: 52, Title: "other"}},
		{{ID: 52, Title: "other"}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := WaitForMatchClose(ctx, m, Match{TitleContains: "target"}, time.Millisecond); err != nil {
		t.Fatalf("wait returned before disappearance: %v", err)
	}
	if m.calls != 2 {
		t.Fatalf("expected one positive probe and one negative probe, got %d", m.calls)
	}
}

func TestWaitForMatchCloseReportsDeadlineWhileMatchingWindowPersists(t *testing.T) {
	m := &sequencedCloseWindowManager{fakeManager: &fakeManager{}, sequence: [][]Info{{{ID: 53, Title: "target app"}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := WaitForMatchClose(ctx, m, Match{TitleContains: "target"}, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("persistent target acknowledged closed, err=%v", err)
	}
	if m.calls < 2 {
		t.Fatalf("persistent target was not rechecked: calls=%d", m.calls)
	}
}

func TestWaitForMatchCloseDoesNotConfuseAnotherWindowWithTarget(t *testing.T) {
	m := &sequencedCloseWindowManager{fakeManager: &fakeManager{}, sequence: [][]Info{{{ID: 72, Title: "unrelated app"}}}}
	if err := WaitForMatchClose(context.Background(), m, Match{TitleContains: "target"}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if m.calls != 1 {
		t.Fatalf("unrelated window kept polling after match absence: calls=%d", m.calls)
	}
}
