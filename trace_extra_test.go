package perfuncted

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// ── newActionTracer ───────────────────────────────────────────────────────────

func TestNewActionTracer_BothNilReturnsNil(t *testing.T) {
	tracer := newActionTracer(nil, nil, 0)
	if tracer != nil {
		t.Fatal("newActionTracer(nil, nil, 0) should return nil")
	}
}

func TestNewActionTracer_WriterOnly(t *testing.T) {
	var buf bytes.Buffer
	tracer := newActionTracer(&buf, nil, 0)
	if tracer == nil {
		t.Fatal("newActionTracer(writer, nil, 0) should not return nil")
	}
}

func TestNewActionTracer_LoggerOnly(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tracer := newActionTracer(nil, logger, 0)
	if tracer == nil {
		t.Fatal("newActionTracer(nil, logger, 0) should not return nil")
	}
}

// ── Tracef ────────────────────────────────────────────────────────────────────

func TestTracef_NilTracerNoPanic(t *testing.T) {
	// A nil *actionTracer must not panic.
	var tracer *actionTracer
	tracer.Tracef("action", "format %s", "arg")
}

func TestTracef_NoFormat(t *testing.T) {
	var buf bytes.Buffer
	tracer := newActionTracer(&buf, nil, 0)
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
	tracer := newActionTracer(nil, logger, 0)
	tracer.Tracef("type", "text=%s", "hello")

	if got := logs.String(); !strings.Contains(got, "perfuncted trace") {
		t.Fatalf("logger-only Tracef: logs = %q, want to contain \"perfuncted trace\"", got)
	}
}

func TestTracef_WriterOnlyNoLogger(t *testing.T) {
	var buf bytes.Buffer
	tracer := newActionTracer(&buf, nil, 0)
	tracer.Tracef("move", "x=%d y=%d", 10, 20)

	if got := buf.String(); !strings.Contains(got, "move x=10 y=20") {
		t.Fatalf("writer-only Tracef: output = %q, want to contain \"move x=10 y=20\"", got)
	}
}

func TestTracef_WithDelay(t *testing.T) {
	var buf bytes.Buffer
	delay := 5 * time.Millisecond
	tracer := newActionTracer(&buf, nil, delay)

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
	tracer := newActionTracer(&buf, nil, 0)
	tracer.w = nil
	tracer.logger = nil
	tracer.Tracef("noop", "should not write")
	if buf.Len() != 0 {
		t.Fatalf("expected no output when both w and logger are nil, got %q", buf.String())
	}
}
