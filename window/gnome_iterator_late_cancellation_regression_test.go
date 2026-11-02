//go:build linux

package window

import (
	"context"
	"errors"
	"testing"
)

const syntheticGnomeWindows = `[{"id":41,"title":"first"},{"id":42,"title":"private-second"}]`

func TestGnomeWindowIteratorRejectsCancelledEvalReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) {
		cancel()
		return syntheticGnomeWindows, nil
	}}
	got, err := mgr.List(ctx)
	if !errors.Is(err, context.Canceled) || len(got) != 0 {
		t.Fatalf("GNOME published windows after a cancelled eval: got=%+v err=%v", got, err)
	}
}

func TestGnomeWindowIteratorStopsPublishingAfterConsumerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) {
		return syntheticGnomeWindows, nil
	}}
	found, cancelled := 0, false
	for info, err := range mgr.IterateWindows(ctx) {
		if errors.Is(err, context.Canceled) {
			if info.ID != 0 {
				t.Fatalf("cancellation leaked window %+v", info)
			}
			cancelled = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		found++
		if found != 1 || info.ID != 41 {
			t.Fatalf("unexpected window after cancellation: %+v", info)
		}
		cancel()
	}
	if found != 1 || !cancelled {
		t.Fatalf("got %d windows, cancellation=%v; want one window then cancellation", found, cancelled)
	}
}

func TestGnomeWindowActionMustNotDispatchAfterPreCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) { calls++; return "ok", nil }}
	err := mgr.ActivateByID(ctx, "41")
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("canceled action dispatched=%d error=%v", calls, err)
	}
}

func TestGnomeWindowActionMustNotReportSuccessAfterCancellationDuringEval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) { calls++; cancel(); return "ok", nil }}
	err := mgr.ActivateByID(ctx, "41")
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("in-flight canceled action reported success: dispatched=%d error=%v", calls, err)
	}
}

func TestGnomeFocusedWindowMustNotLeakAfterCancellationDuringEval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) { cancel(); return "private-focus", nil }}
	title, err := mgr.ActiveTitle(ctx)
	if !errors.Is(err, context.Canceled) || title != "" {
		t.Fatalf("canceled focus leaked title=%q err=%v", title, err)
	}
}

func TestGnomeWindowRequestsRejectCancellationBeforeEvaluatingShell(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) {
		calls++
		return syntheticGnomeWindows, nil
	}}
	if windows, err := mgr.List(ctx); !errors.Is(err, context.Canceled) || len(windows) != 0 {
		t.Fatalf("canceled list result=%+v err=%v", windows, err)
	}
	if title, err := mgr.ActiveTitle(ctx); !errors.Is(err, context.Canceled) || title != "" {
		t.Fatalf("canceled title result=%q err=%v", title, err)
	}
	if calls != 0 {
		t.Fatalf("canceled requests invoked shell %d times", calls)
	}
}

func TestGnomeWindowIteratorDefaultsNilContextAndHonorsValidResult(t *testing.T) {
	calls := 0
	mgr := &GnomeManager{evalJS: func(ctx context.Context, _ string) (string, error) {
		if ctx == nil {
			t.Fatal("nil context forwarded to shell")
		}
		calls++
		return syntheticGnomeWindows, nil
	}}
	// The public API explicitly accepts a missing context and substitutes the
	// default; keep this path covered without passing a nil literal to callers.
	var missingContext context.Context
	windows, err := mgr.List(missingContext)
	if err != nil || len(windows) != 2 || windows[0].ID != 41 || windows[1].ID != 42 {
		t.Fatalf("default context list=%+v err=%v", windows, err)
	}
	if calls != 1 {
		t.Fatalf("expected one shell query, got %d", calls)
	}
}
