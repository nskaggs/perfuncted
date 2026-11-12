//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

type filePickerFailOnRestoreClipboard struct {
	*pftest.Clipboard
	fail error
	sets []string
}

func (c *filePickerFailOnRestoreClipboard) Set(ctx context.Context, text string) error {
	c.sets = append(c.sets, text)
	if len(c.sets) == 2 {
		return c.fail
	}
	return c.Clipboard.Set(ctx, text)
}

func TestSelectFileClipboardFailuresNeverReportSuccess(t *testing.T) {
	file := filepath.Join(t.TempDir(), "fixture.epub")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic clipboard failure")
	cases := []struct {
		name                                        string
		getError, setError, restoreError, typeError bool
		want                                        string
		typed                                       int
		setCount                                    int
		clipboard                                   string
	}{
		{name: "clipboard read fails", getError: true, want: "synthetic clipboard failure", typed: 0, clipboard: "prior"},
		{name: "clipboard staging fails", setError: true, want: "synthetic clipboard failure", typed: 0, clipboard: "prior"},
		{name: "first typing fails and clipboard restored", typeError: true, want: "synthetic typing failure", typed: 1, clipboard: "prior"},
		{name: "clipboard restore failure reported", restoreError: true, want: "restore clipboard", typed: 2, clipboard: file},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := &pftest.Inputter{}
			if tc.typeError {
				in.Err = errors.New("synthetic typing failure")
			}
			mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "dialog-1", Title: "File Upload", Active: true}}, {{NativeID: "dialog-1", Title: "File Upload", Active: true}}, {}}}
			cb := &pftest.Clipboard{Text: "prior"}
			if tc.getError {
				cb.GetErr = failure
			}
			if tc.setError {
				cb.SetErr = failure
			}
			var clipboard interface {
				Get(context.Context) (string, error)
				Set(context.Context, string) error
				Close() error
			} = cb
			if tc.restoreError {
				clipboard = &filePickerFailOnRestoreClipboard{Clipboard: cb, fail: failure}
			}
			stdout, stderr, exit := captureRunIO(t, []string{"input", "select-file", "--title", "File Upload", "--path", file}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, mgr, clipboard), nil }
			})
			if exit == 0 || stdout != "" || !strings.Contains(stderr, tc.want) || len(in.Calls) != tc.typed || cb.Text != tc.clipboard {
				t.Fatalf("case=%s exit=%d stdout=%q stderr=%q calls=%v clipboard=%q want=%q", tc.name, exit, stdout, stderr, in.Calls, cb.Text, tc.want)
			}
		})
	}
}
