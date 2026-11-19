package perfuncted

import (
	"fmt"
	"io"
	"sync"
	"testing"
)

// Diagnostic snapshots retain only the newest bounded set of actions. Older
// action text must not leak into a later failure bundle after eviction.
func TestTraceHistoryEvictsOlderActionsAndReturnsIsolatedSnapshot(t *testing.T) {
	tracer := newActionTracer(io.Discard, nil, 0)
	for i := 0; i < traceHistoryLimit+91; i++ {
		tracer.Tracef("action", "event-%03d", i)
	}
	history := tracer.recent()
	if len(history) != traceHistoryLimit {
		t.Fatalf("history length %d, want %d", len(history), traceHistoryLimit)
	}
	for i, entry := range history {
		want := fmt.Sprintf("action event-%03d", i+91)
		if entry != want {
			t.Fatalf("history[%d]=%q want %q", i, entry, want)
		}
	}
	history[0] = "modified snapshot"
	if tracer.recent()[0] != "action event-091" {
		t.Fatal("modifying diagnostic snapshot changed retained action history")
	}
}

// The caller may retrieve a trace while events are being appended. A snapshot
// must stay coherent and be detached from subsequent evictions.
func TestTraceHistoryConcurrentSnapshotBoundAndNilTracer(t *testing.T) {
	var absent *actionTracer
	if got := absent.recent(); got != nil {
		t.Fatalf("nil tracer leaked history: %v", got)
	}
	tracer := newActionTracer(io.Discard, nil, 0)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < traceHistoryLimit; i++ {
				tracer.Tracef("action", "worker-%d-%d", worker, i)
				snapshot := tracer.recent()
				if len(snapshot) > traceHistoryLimit {
					t.Errorf("unbounded history snapshot: %d", len(snapshot))
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	if len(tracer.recent()) != traceHistoryLimit {
		t.Fatalf("history bound after concurrency: %d", len(tracer.recent()))
	}
}
