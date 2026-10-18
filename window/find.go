package window

import (
	"context"
	"fmt"
	"time"

	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/util"
)

func clampPoll(poll time.Duration) time.Duration {
	if poll <= 0 {
		return 10 * time.Millisecond
	}
	return poll
}

func find(ctx context.Context, m Manager, match Matcher, label string) (Info, error) {
	if err := util.CheckAvailable("window", m); err != nil {
		return Info{}, err
	}
	ctx = contextutil.Default(ctx)
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
	return find(ctx, m, CompileMatch(Match{TitleContains: substr}), substr)
}

// FindByID returns the window with the given stable ID.
func FindByID(ctx context.Context, m Manager, id uint64) (Info, error) {
	return find(ctx, m, CompileMatch(Match{ID: &id}), fmt.Sprintf("id=%d", id))
}

// WaitForMatchClose blocks until no window matches match, or ctx expires.
func WaitForMatchClose(ctx context.Context, m Manager, match Match, poll time.Duration) error {
	ctx = contextutil.Default(ctx)
	if err := util.CheckAvailable("window", m); err != nil {
		return err
	}
	compiled := CompileMatch(match)
	var ticker *time.Ticker
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for window close %q: %w", match.String(), err)
		}
		// An absent window is successful completion, not an exceptional
		// lookup failure. Avoid building an unused formatted not-found error
		// (and a timer) for the common already-closed case.
		found := false
		for w, err := range m.IterateWindows(ctx) {
			if err != nil {
				return err
			}
			if compiled.Matches(w) {
				found = true
				break
			}
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for window close %q: %w", match.String(), err)
		}
		if !found {
			return nil
		}
		if ticker == nil {
			ticker = time.NewTicker(clampPoll(poll))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for window close %q: %w", match.String(), ctx.Err())
		case <-ticker.C:
		}
	}
}
