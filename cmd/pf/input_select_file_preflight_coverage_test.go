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
	"golang.org/x/sys/unix"
)

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
