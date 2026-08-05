package window

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nskaggs/perfuncted/ctxutil"
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
	ctx = ctxutil.Default(ctx)
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

func waitClosedByID(
	ctx context.Context,
	id string,
	infoByID func(context.Context, string) (Info, error),
) error {
	ctx = ctxutil.Default(ctx)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_, err := infoByID(ctx, id)
			switch {
			case err == nil:
				continue
			case errors.Is(err, ErrWindowNotFound):
				return nil
			default:
				return err
			}
		}
	}
}

// WaitForMatchClose blocks until no window matches match, or ctx expires.
func WaitForMatchClose(ctx context.Context, m Manager, match Match, poll time.Duration) error {
	ctx = ctxutil.Default(ctx)
	compiled := CompileMatch(match)
	label := match.String()
	ticker := time.NewTicker(clampPoll(poll))
	defer ticker.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for window close %q: %w", label, err)
		}
		_, err := find(ctx, m, compiled, label)
		if err != nil {
			if errors.Is(err, ErrWindowNotFound) {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for window close %q: %w", label, ctx.Err())
		case <-ticker.C:
		}
	}
}
