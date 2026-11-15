package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

type failedInputReader struct{}

func (failedInputReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic stdin read failure")
}

// Input requests that cannot be fulfilled without a capability must not open
// any desktop, and failed stdin reads must not inject a partial payload.
func TestInputCLIRejectsMissingCapabilityAndFailedStdinWithoutAuthorization(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		input io.Reader
		want  string
	}{
		{"relative pointer needs window", []string{"move-relative"}, nil, "requires window capability"},
		{"window click needs window", []string{"click-window", "--title", "Editor"}, nil, "requires windows capability"},
		{"screenshot click needs outputs", []string{"click-screen", "--x", "0", "--y", "0"}, nil, "requires outputs capability"},
		{"file selection needs dialog", []string{"select-file", "--title", "Open", "--path", file}, nil, "requires window and clipboard capabilities"},
		{"failed stdin read", []string{"type", "--stdin"}, failedInputReader{}, "synthetic stdin read failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) {
				opens++
				return nil, errors.New("desktop authorization attempted")
			}, nil)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs(tc.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			if tc.input != nil {
				cmd.SetIn(tc.input)
			}
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) || opens != 0 {
				t.Fatalf("args=%v unauthorized opens=%d err=%v expected %q", tc.args, opens, err, tc.want)
			}
		})
	}
}
