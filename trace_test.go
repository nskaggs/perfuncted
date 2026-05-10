package perfuncted

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestActionTracerWritesToLoggerAndWriter(t *testing.T) {
	var writer bytes.Buffer
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	tracer := newActionTracer(&writer, logger, 0)
	tracer.Tracef("screen", "grab rect=%s", "(0,0)-(1,1)")

	if got := writer.String(); !strings.Contains(got, "screen grab rect=(0,0)-(1,1)") {
		t.Fatalf("writer trace = %q", got)
	}
	if got := logs.String(); !strings.Contains(got, "perfuncted trace") || !strings.Contains(got, "action=screen") {
		t.Fatalf("logger trace = %q", got)
	}
}
