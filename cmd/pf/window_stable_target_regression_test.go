package main

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
	"strings"
	"testing"
	"time"
)

func TestFindWindowByStableIDHandlesDuplicateTitles(t *testing.T) {
	mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "uuid-one", Title: "Remote Control", ID: 1}, {NativeID: "uuid-two", Title: "Remote Control", ID: 2}}}}
	pf := pftest.New(nil, nil, mgr, nil)
	defer pf.Close()
	target, err := findWindowByStableID(context.Background(), pf.Windows, "uuid-two")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got := target.ID().String(); got != "uuid-two" {
		t.Fatalf("target=%q", got)
	}
	_, err = findWindowByStableID(context.Background(), pf.Windows, "absent")
	if !errors.Is(err, window.ErrWindowNotFound) {
		t.Fatalf("missing id error=%v", err)
	}
}

func TestWindowIDCLICommandsWorkWithDuplicateTitles(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"activate-by-id", []string{"window", "activate-id", "uuid-two"}, "activation requested"},
		{"close-by-id", []string{"window", "close-id", "uuid-two"}, "close requested"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "uuid-one", Title: "Duplicate"}, {NativeID: "uuid-two", Title: "Duplicate"}}}}
			out, errOut, code := captureRunIO(t, tt.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, mgr, nil), nil }
			})
			if code != 0 || !strings.Contains(out, tt.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
			}
			if tt.name == "activate-by-id" && (len(mgr.Activated) != 1 || mgr.Activated[0] != "uuid-two") {
				t.Errorf("activated=%v", mgr.Activated)
			}
		})
	}
}

func TestClickWindowCoordinateConversion(t *testing.T) {
	info := window.Info{X: 100, Y: 200, W: 400, H: 200}
	tests := []struct {
		x, y           int
		kind           input.CoordinateSpaceKind
		scaleX, scaleY float64
		wantX, wantY   int
		wantErr        bool
	}{
		{40, 20, input.CoordinateSpaceLogical, 1, 1, 140, 220, false},
		{40, 20, input.CoordinateSpacePhysical, 1.25, 1.25, 175, 275, false},
		{400, 20, input.CoordinateSpaceLogical, 1, 1, 0, 0, true},
		{-1, 20, input.CoordinateSpaceLogical, 1, 1, 0, 0, true},
		{40, 20, "", 0, 0, 0, 0, true},
	}
	for _, tt := range tests {
		x, y, err := windowRelativePoint(info, tt.x, tt.y, input.CoordinateSpaceInfo{Kind: tt.kind, ScaleX: tt.scaleX, ScaleY: tt.scaleY})
		if (err != nil) != tt.wantErr || (!tt.wantErr && (x != tt.wantX || y != tt.wantY)) {
			t.Errorf("%+v: point=(%d,%d) err=%v", tt, x, y, err)
		}
	}
}

func TestFindWindowByStableIDAcceptsX11PrintedHexID(t *testing.T) {
	mgr := &pftest.Manager{Lists: [][]window.Info{{{ID: 0xe00017, Title: "X11 window"}, {ID: 0xe00018, Title: "other"}}}}
	pf := pftest.New(nil, nil, mgr, nil)
	defer pf.Close()
	target, err := findWindowByStableID(context.Background(), pf.Windows, "0xe00017")
	if err != nil || target.CachedInfo().ID != 0xe00017 {
		t.Fatalf("target=%v err=%v", target, err)
	}
}

func TestWaitWindowGoneRequiresConfirmedDisappearance(t *testing.T) {
	mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "dialog-uuid", Title: "File Upload"}}, {{NativeID: "dialog-uuid", Title: "File Upload"}}, {}}}
	pf := pftest.New(nil, nil, mgr, nil)
	defer pf.Close()
	if err := waitWindowGone(context.Background(), pf.Windows, "dialog-uuid", 100*time.Millisecond, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	stuck := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "dialog-uuid", Title: "File Upload"}}}}
	pf2 := pftest.New(nil, nil, stuck, nil)
	defer pf2.Close()
	if err := waitWindowGone(context.Background(), pf2.Windows, "dialog-uuid", 15*time.Millisecond, 5*time.Millisecond); err == nil {
		t.Fatal("ignored still open file chooser")
	}
}
