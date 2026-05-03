package perfuncted

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type actionTracer struct {
	w     io.Writer
	delay time.Duration
	mu    sync.Mutex
}

func newActionTracer(w io.Writer, delay time.Duration) *actionTracer {
	if w == nil {
		return nil
	}
	return &actionTracer{w: w, delay: delay}
}

func (t *actionTracer) Tracef(action, format string, args ...any) {
	if t == nil || t.w == nil {
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
	_, _ = fmt.Fprintln(t.w, msg.String())
	if t.delay > 0 {
		time.Sleep(t.delay)
	}
}
