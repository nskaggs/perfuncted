package window

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// FindByTitle returns the first window whose title contains substr
// (case-insensitive). Error messages are standardized for callers.
func FindByTitle(ctx context.Context, m Manager, substr string) (Info, error) {
	wins, err := m.List(ctx)
	if err != nil {
		return Info{}, err
	}
	lc := strings.ToLower(substr)
	for _, w := range wins {
		if strings.Contains(strings.ToLower(w.Title), lc) {
			return w, nil
		}
	}
	return Info{}, fmt.Errorf("window matching %q not found", substr)
}

// WaitFor blocks until a window matching pattern is found, or ctx expires.
func WaitFor(ctx context.Context, m Manager, pattern string, poll time.Duration) (Info, error) {
	for {
		info, err := FindByTitle(ctx, m, pattern)
		if err == nil {
			return info, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Info{}, fmt.Errorf("wait for window %q: %w", pattern, ctx.Err())
		case <-timer.C:
		}
	}
}

// WaitForClose blocks until no window matches pattern, or ctx expires.
func WaitForClose(ctx context.Context, m Manager, pattern string, poll time.Duration) error {
	for {
		_, err := FindByTitle(ctx, m, pattern)
		if err != nil {
			return nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for window close %q: %w", pattern, ctx.Err())
		case <-timer.C:
		}
	}
}
