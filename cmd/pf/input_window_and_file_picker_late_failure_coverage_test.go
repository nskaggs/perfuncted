package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

type syntheticWindowClickSyncFailure struct {
	*coverageTitleClickInput
	failure error
}

func (s *syntheticWindowClickSyncFailure) Sync(context.Context) error { return s.failure }

type syntheticPickerActivationFailure struct {
	*pftest.Manager
	failure error
}

func (s *syntheticPickerActivationFailure) ActivateByID(context.Context, string) error {
	return s.failure
}

func TestClickWindowRejectsFailedSynchronizationWithoutSuccessOutput(t *testing.T) {
	failure := errors.New("synthetic synchronization refused")
	in := &syntheticWindowClickSyncFailure{
		coverageTitleClickInput: &coverageTitleClickInput{
			Inputter: &pftest.Inputter{},
			space:    input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1},
		},
		failure: failure,
	}
	manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "editor", Title: "Editor", X: 10, Y: 20, W: 100, H: 100}}}}
	opener := func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, manager, nil), nil }
	cmd := inputCmd(opener, &cliConfig{sync: true}, opener)
	cmd.SetArgs([]string{"click-window", "--id", "editor", "--x", "3", "--y", "5"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("failed synchronization not reported: %v", err)
	}
	if stdout.Len() != 0 || len(in.Inputter.Calls) != 1 || in.Inputter.Calls[0] != "click:13,25" {
		t.Fatalf("unexpected output=%q input calls=%v", stdout.String(), in.Inputter.Calls)
	}
}

func TestFilePickerStopsOnMissingTargetOrFailedActivation(t *testing.T) {
	failure := errors.New("synthetic activation denied")
	destination := filepath.Join(t.TempDir(), "test-file.txt")
	if err := os.WriteFile(destination, []byte("private synthetic fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		manager   window.Manager
		wantError error
	}{
		{name: "missing target", manager: &pftest.Manager{Lists: [][]window.Info{{}}}, wantError: window.ErrWindowNotFound},
		{name: "activation rejected", manager: &syntheticPickerActivationFailure{Manager: &pftest.Manager{Lists: [][]window.Info{{{NativeID: "chooser", Title: "File Upload", Active: true}}}}, failure: failure}, wantError: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := &pftest.Inputter{}
			cb := &pftest.Clipboard{Text: "previous clipboard"}
			opener := func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, tc.manager, cb), nil }
			cmd := inputCmd(opener, nil, nil, nil, opener)
			cmd.SetArgs([]string{"select-file", "--title", "File Upload", "--path", destination})
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("target failure not reported: %v, wanted %v", err, tc.wantError)
			}
			if stdout.Len() != 0 || len(in.Calls) != 0 || cb.Text != "previous clipboard" {
				t.Fatalf("rejected file selection leaked output=%q, input=%v, clipboard=%q", stdout.String(), in.Calls, cb.Text)
			}
		})
	}
}

func TestRelativePointerMovePropagatesSecondaryOpenerFailure(t *testing.T) {
	failure := errors.New("synthetic window authorization denied")
	openCalls := 0
	opener := func(context.Context) (*perfuncted.Session, error) { openCalls++; return nil, failure }
	cmd := inputCmd(nil, nil, opener)
	cmd.SetArgs([]string{"move-relative", "--dx", "4", "--dy", "5"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, failure) || openCalls != 1 {
		t.Fatalf("secondary authorization failure not propagated: err=%v openCalls=%d", err, openCalls)
	}
}
