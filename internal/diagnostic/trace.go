package diagnostic

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type Tracer struct {
	w       io.Writer
	logger  *slog.Logger
	delay   time.Duration
	history []string
	// mu serializes writes to the underlying writer.
	mu sync.Mutex
}

const traceHistoryLimit = 256

func NewTracer(w io.Writer, logger *slog.Logger, delay time.Duration) *Tracer {
	if w == nil && logger == nil {
		return nil
	}
	return &Tracer{w: w, logger: logger, delay: delay}
}

func (t *Tracer) Tracef(action, format string, args ...any) {
	if t == nil || (t.w == nil && t.logger == nil) {
		return
	}
	var msg strings.Builder
	msg.WriteString(action)
	if format != "" {
		msg.WriteByte(' ')
		fmt.Fprintf(&msg, format, args...)
	}
	text := strings.TrimSpace(msg.String())

	t.mu.Lock()
	t.history = append(t.history, text)
	if len(t.history) > traceHistoryLimit {
		t.history = t.history[len(t.history)-traceHistoryLimit:]
	}
	if t.logger != nil {
		t.logger.Debug("perfuncted trace", "action", action, "message", text)
	}
	if t.w != nil {
		_, _ = fmt.Fprintln(t.w, text)
	}
	t.mu.Unlock()
	if t.delay > 0 {
		time.Sleep(t.delay)
	}
}

func (t *Tracer) Recent() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.history...)
}
