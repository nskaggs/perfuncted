//go:build linux

package main

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/pftest"
	"github.com/nskaggs/perfuncted/v2/window"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestSelectFilePreflightRejectsUnsafePathsBeforeDesktopAuthorization(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "fixture.txt")
	if err := os.WriteFile(regular, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "special.fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		args  []string
		want  string
		opens int
	}{
		{"missing selector", []string{"--path", regular}, "exactly one", 0},
		{"both selectors", []string{"--id", "dialog", "--title", "chooser", "--path", regular}, "exactly one", 0},
		{"relative file", []string{"--id", "dialog", "--path", "fixture.txt"}, "absolute", 0},
		{"missing file", []string{"--id", "dialog", "--path", filepath.Join(dir, "missing.txt")}, "file chooser path", 0},
		{"directory", []string{"--id", "dialog", "--path", dir}, "regular file, not a directory", 0},
		{"special file", []string{"--id", "dialog", "--path", fifo}, "not a regular file", 0},
		{"valid file reaches authorization", []string{"--id", "dialog", "--path", regular}, "synthetic authorization refused", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			args := append([]string{"input", "select-file"}, tc.args...)
			out, stderr, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("synthetic authorization refused")
				}
			})
			if exit == 0 || out != "" || opens != tc.opens || !strings.Contains(stderr, tc.want) {
				t.Fatalf("args=%v exit=%d opens=%d out=%q stderr=%q want=%q", args, exit, opens, out, stderr, tc.want)
			}
		})
	}
}
