package diagnostic

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── NewTracer ───────────────────────────────────────────────────────────

func TestNewActionTracer_BothNilReturnsNil(t *testing.T) {
	tracer := NewTracer(nil, nil, 0)
	if tracer != nil {
		t.Fatal("NewTracer(nil, nil, 0) should return nil")
	}
}

func TestNewActionTracer_WriterOnly(t *testing.T) {
	var buf bytes.Buffer
	tracer := NewTracer(&buf, nil, 0)
	if tracer == nil {
		t.Fatal("NewTracer(writer, nil, 0) should not return nil")
	}
}

func TestNewActionTracer_LoggerOnly(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tracer := NewTracer(nil, logger, 0)
	if tracer == nil {
		t.Fatal("NewTracer(nil, logger, 0) should not return nil")
	}
}

// ── Tracef ────────────────────────────────────────────────────────────────────

func TestTracef_NilTracerNoPanic(t *testing.T) {
	// A nil *Tracer must not panic.
	var tracer *Tracer
	tracer.Tracef("action", "format %s", "arg")
}

func TestTracef_NoFormat(t *testing.T) {
	var buf bytes.Buffer
	tracer := NewTracer(&buf, nil, 0)
	tracer.Tracef("click", "")
	got := buf.String()
	if !strings.Contains(got, "click") {
		t.Fatalf("Tracef with empty format: output = %q, want to contain \"click\"", got)
	}
	// Should not append a trailing space after the action name when format is "".
	if strings.Contains(got, "click ") {
		t.Errorf("Tracef with empty format: unexpected trailing space in %q", got)
	}
}

func TestTracef_LoggerOnlyNoWriter(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tracer := NewTracer(nil, logger, 0)
	tracer.Tracef("type", "text=%s", "hello")

	if got := logs.String(); !strings.Contains(got, "perfuncted trace") {
		t.Fatalf("logger-only Tracef: logs = %q, want to contain \"perfuncted trace\"", got)
	}
}

func TestTracef_WriterOnlyNoLogger(t *testing.T) {
	var buf bytes.Buffer
	tracer := NewTracer(&buf, nil, 0)
	tracer.Tracef("move", "x=%d y=%d", 10, 20)

	if got := buf.String(); !strings.Contains(got, "move x=10 y=20") {
		t.Fatalf("writer-only Tracef: output = %q, want to contain \"move x=10 y=20\"", got)
	}
}

func TestTracef_WithDelay(t *testing.T) {
	var buf bytes.Buffer
	delay := 5 * time.Millisecond
	tracer := NewTracer(&buf, nil, delay)

	start := time.Now()
	tracer.Tracef("wait", "")
	elapsed := time.Since(start)

	if elapsed < delay {
		t.Fatalf("Tracef with delay: elapsed %v < delay %v", elapsed, delay)
	}
	if got := buf.String(); !strings.Contains(got, "wait") {
		t.Fatalf("Tracef with delay: output = %q, want to contain \"wait\"", got)
	}
}

func TestTracef_BothNilFieldsOnExistingTracer(t *testing.T) {
	// Create a tracer then zero out its fields. Tracef should return early.
	var buf bytes.Buffer
	tracer := NewTracer(&buf, nil, 0)
	tracer.w = nil
	tracer.logger = nil
	tracer.Tracef("noop", "should not write")
	if buf.Len() != 0 {
		t.Fatalf("expected no output when both w and logger are nil, got %q", buf.String())
	}
}

// Diagnostic snapshots retain only the newest bounded set of actions. Older
// action text must not leak into a later failure bundle after eviction.
func TestTraceHistoryEvictsOlderActionsAndReturnsIsolatedSnapshot(t *testing.T) {
	tracer := NewTracer(io.Discard, nil, 0)
	for i := 0; i < traceHistoryLimit+91; i++ {
		tracer.Tracef("action", "event-%03d", i)
	}
	history := tracer.Recent()
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
	if tracer.Recent()[0] != "action event-091" {
		t.Fatal("modifying diagnostic snapshot changed retained action history")
	}
}

// The caller may retrieve a trace while events are being appended. A snapshot
// must stay coherent and be detached from subsequent evictions.
func TestTraceHistoryConcurrentSnapshotBoundAndNilTracer(t *testing.T) {
	var absent *Tracer
	if got := absent.Recent(); got != nil {
		t.Fatalf("nil tracer leaked history: %v", got)
	}
	tracer := NewTracer(io.Discard, nil, 0)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < traceHistoryLimit; i++ {
				tracer.Tracef("action", "worker-%d-%d", worker, i)
				snapshot := tracer.Recent()
				if len(snapshot) > traceHistoryLimit {
					t.Errorf("unbounded history snapshot: %d", len(snapshot))
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	if len(tracer.Recent()) != traceHistoryLimit {
		t.Fatalf("history bound after concurrency: %d", len(tracer.Recent()))
	}
}

func TestActionTracerWritesToLoggerAndWriter(t *testing.T) {
	var writer bytes.Buffer
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	tracer := NewTracer(&writer, logger, 0)
	tracer.Tracef("screen", "grab rect=%s", "(0,0)-(1,1)")

	if got := writer.String(); !strings.Contains(got, "screen grab rect=(0,0)-(1,1)") {
		t.Fatalf("writer trace = %q", got)
	}
	if got := logs.String(); !strings.Contains(got, "perfuncted trace") || !strings.Contains(got, "action=screen") {
		t.Fatalf("logger trace = %q", got)
	}
}
