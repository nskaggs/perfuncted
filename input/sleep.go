package input

import (
	"context"
	"time"

	"github.com/nskaggs/perfuncted/internal/contextutil"
)

func sleepContext(ctx context.Context, d time.Duration) error {
	ctx = contextutil.Default(ctx)
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
