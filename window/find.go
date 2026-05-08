package window

import (
	"context"
	"fmt"
	"time"
)

// Find returns the first window matching match.
func Find(ctx context.Context, m Manager, match Match) (Info, error) {
	return find(ctx, m, match, match.String())
}

func find(ctx context.Context, m Manager, match Match, label string) (Info, error) {
	for w, err := range m.IterateWindows(ctx) {
		if err != nil {
			return Info{}, err
		}
		if match.Matches(w) {
			return w, nil
		}
	}
	return Info{}, fmt.Errorf("window matching %q not found: %w", label, ErrWindowNotFound)
}

// FindByTitle returns the first window whose title contains substr
// (case-insensitive). Error messages are standardized for callers.
func FindByTitle(ctx context.Context, m Manager, substr string) (Info, error) {
	return find(ctx, m, Match{TitleContains: substr}, substr)
}

// WaitFor blocks until a window matching pattern is found, or ctx expires.
func WaitFor(ctx context.Context, m Manager, pattern string, poll time.Duration) (Info, error) {
	return WaitForMatch(ctx, m, Match{TitleContains: pattern}, poll)
}

// WaitForMatch blocks until a window matching match is found, or ctx expires.
func WaitForMatch(ctx context.Context, m Manager, match Match, poll time.Duration) (Info, error) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		info, err := Find(ctx, m, match)
		if err == nil {
			return info, nil
		}
		select {
		case <-ctx.Done():
			return Info{}, fmt.Errorf("wait for window %q: %w", match.String(), ctx.Err())
		case <-ticker.C:
		}
	}
}

// WaitForClose blocks until no window matches pattern, or ctx expires.
func WaitForClose(ctx context.Context, m Manager, pattern string, poll time.Duration) error {
	return WaitForMatchClose(ctx, m, Match{TitleContains: pattern}, poll)
}

// WaitForMatchClose blocks until no window matches match, or ctx expires.
func WaitForMatchClose(ctx context.Context, m Manager, match Match, poll time.Duration) error {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		_, err := Find(ctx, m, match)
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for window close %q: %w", match.String(), ctx.Err())
		case <-ticker.C:
		}
	}
}
