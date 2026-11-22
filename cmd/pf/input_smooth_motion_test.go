package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/pftest"
)

func TestInputSmoothCLIRejectsInvalidFlagsBeforeSession(t *testing.T) {
	for _, args := range [][]string{
		{"move", "--x=5", "--duration=-1s"},
		{"move", "--x=5", "--duration=11s"},
		{"move", "--x=5", "--duration=20ms", "--easing=nope"},
		{"move", "--x=5", "--duration=20ms", "--curve=NaN"},
		{"move", "--x=5", "--duration=20ms", "--variation=17"},
		{"move", "--x=5", "--duration=20ms", "--from-x=1"},
		{"drag-and-drop", "--duration=20ms", "--easing=nope"},
		{"drag-and-drop", "--duration=10ms", "--curve=0.8"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			opens := 0
			cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) {
				opens++
				return pftest.New(nil, &pftest.Inputter{}, nil, nil), nil
			}, nil)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(args)
			if err := cmd.ExecuteContext(context.Background()); err == nil {
				t.Fatal("accepted invalid motion")
			}
			if opens != 0 || out.Len() != 0 {
				t.Fatalf("session opened or success output: opens=%d out=%q", opens, out.String())
			}
		})
	}
}
func TestInputSmoothCLIEmitsSingleOutcome(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		last string
		out  string
		drag bool
	}{
		{"immediate", []string{"move", "--x=7", "--y=9"}, "move:7,9", "moved to 7,9", false},
		{"smooth-explicit", []string{"move", "--from-x=1", "--from-y=2", "--x=70", "--y=90", "--duration=40ms", "--easing=ease-in-out", "--seed=3", "--variation=2"}, "move:70,90", "moved to 70,90", false},
		{"smooth-drag", []string{"drag-and-drop", "--x1=1", "--y1=2", "--x2=70", "--y2=90", "--duration=40ms", "--curve=0.2"}, "mouseup", "dragged 1,2 to 70,90", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &pftest.Inputter{}
			cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, backend, nil, nil), nil }, nil)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			calls := backend.Calls
			if len(calls) == 0 || calls[len(calls)-1] != tc.last {
				t.Fatalf("calls=%v", calls)
			}
			if strings.TrimSpace(out.String()) != tc.out {
				t.Fatalf("output=%q", out.String())
			}
			if tc.drag {
				down, up := 0, 0
				for _, c := range calls {
					if c == "mousedown" {
						down++
					}
					if c == "mouseup" {
						up++
					}
				}
				if down != 1 || up != 1 || len(calls) < 4 {
					t.Fatalf("drag calls=%v", calls)
				}
			}
		})
	}
}
func TestInputSmoothCLIUnknownOriginDoesNotClaimSuccess(t *testing.T) {
	backend := &pftest.Inputter{Err: input.ErrNotSupported}
	cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, backend, nil, nil), nil }, nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"move", "--x=10", "--y=20", "--duration=40ms"})
	err := cmd.ExecuteContext(context.Background())
	if !errors.Is(err, input.ErrNotSupported) || len(backend.Calls) != 0 || out.Len() != 0 {
		t.Fatalf("unknown origin err=%v calls=%v out=%q", err, backend.Calls, out.String())
	}
}
