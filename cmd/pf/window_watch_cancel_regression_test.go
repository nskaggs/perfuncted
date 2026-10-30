package main

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

type cancellingWatchManager struct {
	*pftest.Manager
	cancel context.CancelFunc
}

func (m *cancellingWatchManager) List(ctx context.Context) ([]window.Info, error) {
	m.cancel()
	return nil, ctx.Err()
}

func TestWindowWatchCancellationDuringEnumerationExitsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &cancellingWatchManager{Manager: &pftest.Manager{}, cancel: cancel}
	stdout, stderr, code := captureRunIOContext(ctx, t, []string{"window", "watch", "--output", "json"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return pftest.New(nil, nil, manager, nil), nil
		}
	})
	if code != 0 {
		t.Fatalf("canceled watch exit=%d stdout=%q stderr=%q; want successful shutdown", code, stdout, stderr)
	}
}
