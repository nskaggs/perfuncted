package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

type coverageTitleClickInput struct {
	*pftest.Inputter
	space    input.CoordinateSpaceInfo
	spaceErr error
}

func (i *coverageTitleClickInput) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return i.space, i.spaceErr
}

func TestInputClickWindowTitleRequiresOneCurrentTargetAndSafeDispatch(t *testing.T) {
	coordinateError := errors.New("synthetic coordinate metadata unavailable")
	clickError := errors.New("synthetic click transport failure")
	target := window.Info{NativeID: "editor-window", Title: "Editor", X: 100, Y: 200, W: 400, H: 300}
	space := input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}
	tests := []struct {
		name        string
		lists       [][]window.Info
		spaceErr    error
		inputErr    error
		wantSuccess bool
		wantErr     string
		wantClick   string
	}{
		{
			name:        "unique title maps into current window",
			lists:       [][]window.Info{{target}, {target}},
			wantSuccess: true,
			wantClick:   "click:120,230",
		},
		{
			name:      "ambiguous title refuses to choose a window",
			lists:     [][]window.Info{{target, {NativeID: "another-window", Title: "Editor settings"}}},
			wantErr:   "ambiguous",
			wantClick: "",
		},
		{
			name:      "window disappearance before geometry refresh refuses click",
			lists:     [][]window.Info{{target}, {}},
			wantErr:   "not found",
			wantClick: "",
		},
		{
			name:      "unavailable coordinate contract refuses click",
			lists:     [][]window.Info{{target}, {target}},
			spaceErr:  coordinateError,
			wantErr:   "cannot safely map window coordinates",
			wantClick: "",
		},
		{
			name:      "click transport failure is returned without success output",
			lists:     [][]window.Info{{target}, {target}},
			inputErr:  clickError,
			wantErr:   clickError.Error(),
			wantClick: "click:120,230",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := &coverageTitleClickInput{Inputter: &pftest.Inputter{Err: tc.inputErr}, space: space, spaceErr: tc.spaceErr}
			manager := &pftest.Manager{Lists: tc.lists}
			stdout, stderr, code := captureRunIO(t, []string{"input", "click-window", "--title", "Editor", "--x", "20", "--y", "30"}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(nil, in, manager, nil), nil
				}
			})
			if tc.wantSuccess {
				if code != 0 || !strings.Contains(stdout, "click event sent to window editor-window") {
					t.Fatalf("unique title click: code=%d stdout=%q stderr=%q", code, stdout, stderr)
				}
			} else {
				if code == 0 || !strings.Contains(stderr, tc.wantErr) {
					t.Fatalf("unsafe or failed click returned code=%d stdout=%q stderr=%q, want error containing %q", code, stdout, stderr, tc.wantErr)
				}
				if strings.Contains(stdout, "click event sent") {
					t.Fatalf("failed click reported success: %q", stdout)
				}
			}
			calls := strings.Join(in.Calls, ",")
			if tc.wantClick == "" {
				if strings.Contains(calls, "click:") {
					t.Fatalf("rejected click reached input backend: %q", calls)
				}
			} else if !strings.Contains(calls, tc.wantClick) {
				t.Fatalf("input calls=%q, want click %q", calls, tc.wantClick)
			}
		})
	}
}
