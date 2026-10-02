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
	if err == nil || !strings.Contains(err.Error(), "consecutive and") || !strings.Contains(err.Error(), "total evaluation errors") {
		t.Fatalf("Wait error = %v; want sustained-failure abort", err)
	}
}

// alternatingWindowManager fails every other List call forever. Half its reads
// answer, so a wait that resets its failure count on every successful read can
// never accumulate enough consecutive failures to give up.
type alternatingWindowManager struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (m *alternatingWindowManager) List(context.Context) ([]window.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls%2 == 0 {
		return nil, m.err
	}
	return nil, nil
}

func (m *alternatingWindowManager) IterateWindows(
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

func (m *alternatingWindowManager) ActiveTitle(context.Context) (string, error) {
	return "", nil
}

func (m *alternatingWindowManager) Close() error { return nil }

// A source that answers only half the time is still mostly unreadable, and the
// caller has to be told so rather than being waited out to its own deadline.
func TestSessionWaitGivesUpOnFailuresInterleavedWithSuccessfulReads(t *testing.T) {
	manager := &alternatingWindowManager{err: fmt.Errorf("window/sway: get_tree: read unix: i/o timeout")}
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	// The deadline is far longer than the bound needs, so an implementation that
	// waits the deadline out is reported by the elapsed check instead.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	start := time.Now()
	err := session.Wait(ctx, WindowExists(WindowMatch{TitleExact: "Ready"}), WaitEvery(time.Millisecond))
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "total evaluation errors") {
		t.Fatalf("Wait error = %v; want an abort reporting the wait-wide failure total", err)
	}
	if !errors.Is(err, manager.err) {
		t.Fatalf("Wait error = %v; want the underlying read failure attached", err)
	}
	if elapsed >= 30*time.Second {
		t.Fatalf("Wait took %v; it waited out its deadline instead of reporting unreadable state", elapsed)
	}
	manager.mu.Lock()
	calls := manager.calls
	manager.mu.Unlock()
	if calls > 2*waitEvaluateTotalLimit+2 {
		t.Fatalf("List calls = %d, want the wait bounded by the failure total", calls)
	}
}

// The consecutive count is not itself a bound in elapsed time: a wait driven by
// window events evaluates far more often than one driven by its poll interval.
// A run of failures that stays unanswered long enough must be reported even
// though its count is well under the consecutive limit.
func TestWaitEvaluateFailuresBoundStreakByElapsedTime(t *testing.T) {
	start := time.Unix(0, 0)
	now := start
	failures := waitEvaluateFailures{now: func() time.Time { return now }}

	if exhausted, _, _ := failures.record(); exhausted {
		t.Fatal("first failure exhausted the tolerance")
	}
	// Failures spread across less than the budget stay tolerated, which is the
	// point: their count alone would not have stopped them.
	for range 8 {
		now = now.Add(waitEvaluateStreakBudget / 16)
		if exhausted, consecutive, _ := failures.record(); exhausted {
			t.Fatalf("failure %d recorded %v into the streak exhausted the tolerance early", consecutive, now.Sub(start))
		}
	}
	now = now.Add(waitEvaluateStreakBudget)
	exhausted, consecutive, _ := failures.record()
	if consecutive >= waitEvaluateFailureLimit {
		t.Fatalf("consecutive failures = %d; the test must not reach the count limit", consecutive)
	}
	if !exhausted {
		t.Fatalf("record %v into a streak of %d consecutive failures = exhausted %t; want the streak budget to end the run",
			now.Sub(start), consecutive, exhausted)
	}
}

// A run of failures that is answered in time resets the consecutive count but
// not the wait-wide total.
func TestWaitEvaluateFailuresResetEndsStreakNotTotal(t *testing.T) {
	failures := waitEvaluateFailures{}
	for range 3 {
		if exhausted, _, _ := failures.record(); exhausted {
			t.Fatal("few failures exhausted the tolerance")
		}
	}
	failures.reset()
	if failures.consecutive != 0 || failures.total != 3 || !failures.since.IsZero() {
		t.Fatalf("after reset = consecutive %d, total %d, since %v; want 0, 3, zero",
			failures.consecutive, failures.total, failures.since)
	}
}
