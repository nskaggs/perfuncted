package perfuncted

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type actionTracer struct {
	w      io.Writer
	logger *slog.Logger
	delay  time.Duration
	mu     sync.Mutex
}

func newActionTracer(w io.Writer, logger *slog.Logger, delay time.Duration) *actionTracer {
	if w == nil && logger == nil {
		return nil
	}
	return &actionTracer{w: w, logger: logger, delay: delay}
}

func (t *actionTracer) Tracef(action, format string, args ...any) {
	if t == nil || (t.w == nil && t.logger == nil) {
		return
	}
	var msg strings.Builder
	msg.WriteString(action)
	if format != "" {
		msg.WriteByte(' ')
		msg.WriteString(fmt.Sprintf(format, args...))
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.logger != nil {
		t.logger.Debug("perfuncted trace", "action", action, "message", strings.TrimSpace(msg.String()))
	}
	if t.w != nil {
		_, _ = fmt.Fprintln(t.w, msg.String())
	}
	if t.delay > 0 {
		time.Sleep(t.delay)
	}
}
