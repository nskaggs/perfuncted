package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

// Denied session opening must fail both clipboard commands before invoking a
// clipboard device. A failed clipboard read must likewise not produce stale
// or partial clipboard contents on stdout.
func TestClipboardCommandsFailClosedOnOpenAndReadErrors(t *testing.T) {
	denied := errors.New("clipboard session authorization denied")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"get", []string{"get"}},
		{"set", []string{"set", "private text"}},
	} {
		t.Run("open denied "+tc.name, func(t *testing.T) {
			cmd := clipboardCmd(func(context.Context) (*perfuncted.Session, error) { return nil, denied })
			var stdout, stderr bytes.Buffer
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); !errors.Is(err, denied) {
				t.Fatalf("clipboard %s open error=%v, want denial", tc.name, err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("denied %s wrote clipboard data: %q", tc.name, stdout.String())
			}
		})
	}
	t.Run("get denied by backend", func(t *testing.T) {
		cb := &pftest.Clipboard{Text: "must not leak", GetErr: denied}
		cmd := clipboardCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, nil, cb), nil })
		var stdout, stderr bytes.Buffer
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"get"})
		if err := cmd.Execute(); !errors.Is(err, denied) {
			t.Fatalf("clipboard read error=%v, want denial", err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("failed clipboard read disclosed data: %q", stdout.String())
		}
	})
}
