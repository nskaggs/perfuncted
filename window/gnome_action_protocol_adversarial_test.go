package window

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestGnomeManagerSyntheticWindowLifecycleAndActionIsolation(t *testing.T) {
	const list = `[{"id":17,"title":"Editor","class":"org.example.Editor","pid":153,"x":12,"y":18,"w":900,"h":650},{"id":18,"title":"Other","pid":19,"x":1,"y":2,"w":400,"h":300}]`
	type result struct {
		script string
		err    error
	}
	captures := []result{}
	manager := &GnomeManager{evalJS: func(_ context.Context, script string) (string, error) {
		captures = append(captures, result{script: script})
		if strings.Contains(script, "JSON.stringify(") {
			return list, nil
		}
		if strings.Contains(script, "get_focus_window") {
			return "Editor", nil
		}
		return "ok", nil
	}}
	windows, err := manager.List(context.Background())
	if err != nil || len(windows) != 2 || windows[0].Title != "Editor" || windows[0].X != 12 || windows[0].W != 900 || windows[1].ID != 18 {
		t.Fatalf("unexpected GNOME windows: %+v %v", windows, err)
	}
	for _, tc := range []struct {
		name, needle string
		action       func() error
	}{
		{"activate", "w.activate(global.get_current_time())", func() error { return manager.ActivateByID(context.Background(), "17") }},
		{"move", "w.move_frame(true, -23, 48)", func() error { return manager.MoveByID(context.Background(), "17", -23, 48) }},
		{"resize", "w.move_resize_frame(true, w.get_frame_rect().x, w.get_frame_rect().y, 320, 200)", func() error { return manager.ResizeByID(context.Background(), "17", 320, 200) }},
		{"close", "w.delete(global.get_current_time())", func() error { return manager.CloseWindowByID(context.Background(), "17") }},
		{"minimize", "w.minimize()", func() error { return manager.MinimizeByID(context.Background(), "17") }},
		{"maximize", "w.maximize(3)", func() error { return manager.MaximizeByID(context.Background(), "17") }},
		{"restore", "w.unminimize(); w.unmaximize(3)", func() error { return manager.RestoreByID(context.Background(), "17") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(captures)
			if err := tc.action(); err != nil {
				t.Fatal(err)
			}
			if len(captures) != before+1 {
				t.Fatal("window action did not reach synthetic shell")
			}
			script := captures[before].script
			if !strings.Contains(script, tc.needle) || !strings.Contains(script, "get_stable_sequence()===17") || !strings.Contains(script, "if(!w)") {
				t.Fatalf("unsafe/mismatched script for %s: %s", tc.name, script)
			}
		})
	}
	if title, err := manager.ActiveTitle(context.Background()); err != nil || title != "Editor" {
		t.Fatalf("active title %q %v", title, err)
	}
	if info, err := manager.InfoByID(context.Background(), "18"); err != nil || info.Title != "Other" {
		t.Fatalf("InfoByID wrong window: %+v %v", info, err)
	}
	if _, err := manager.InfoByID(context.Background(), "999"); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("unknown ID returned %v", err)
	}
	if err := manager.FullscreenByID(context.Background(), "17"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("fullscreen=%v", err)
	}
	if err := manager.UnfullscreenByID(context.Background(), "17"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unfullscreen=%v", err)
	}
	if ops := strings.Join(manager.SupportedOperations(), ","); !strings.Contains(ops, "activate") || strings.Contains(ops, "fullscreen") {
		t.Fatalf("advertised unsupported operation: %s", ops)
	}
	if err := manager.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGnomeManagerSyntheticMalformedAndErrorPaths(t *testing.T) {
	failures := errors.New("GNOME shell gone")
	for _, tc := range []struct {
		name, reply string
		evalErr     error
		wantErr     error
	}{
		{"invalid JSON", "garbled", nil, nil},
		{"empty JSON", "", nil, nil},
		{"shell failure", "", failures, failures},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) { return tc.reply, tc.evalErr }}
			if _, err := mgr.List(context.Background()); err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("malformed/error list accepted: %v", err)
			}
			if _, err := mgr.InfoByID(context.Background(), "17"); err == nil {
				t.Fatal("malformed info accepted")
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	mgr := &GnomeManager{evalJS: func(ctx context.Context, script string) (string, error) { return "", ctx.Err() }}
	if err := mgr.ActivateByID(canceled, "17"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	for _, id := range []string{"-1", "1;global.main()", "abc", strconv.FormatUint(uint64(math.MaxUint64), 10)} {
		if err := mgr.ActivateByID(context.Background(), id); err == nil {
			t.Fatalf("unsafe GNOME ID accepted: %q", id)
		}
	}
	if err := mgr.ActivateByID(context.Background(), strconv.FormatUint(maxGnomeJSSafeInteger, 10)); err != nil {
		t.Fatalf("valid JavaScript safe integer was rejected: %v", err)
	}
}
