package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

func TestWindowWaitForZeroPollUsesSafeDefaultAndFindMissingRemainsError(t *testing.T) {
	backend := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "one", Title: "Editor", X: 1, Y: 2, W: 100, H: 80}}}}
	for _, tc := range []struct {
		name      string
		args      []string
		want      string
		wantError bool
	}{
		{name: "zero interval", args: []string{"wait-for", "title=Editor", "--poll=0s", "--timeout=200ms"}, want: "Editor"},
		{name: "find missing", args: []string{"find-by-title", "Missing"}, want: "not found", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, backend, nil), nil }, nil)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs(tc.args)
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			err := cmd.ExecuteContext(context.Background())
			if tc.wantError {
				if !errors.Is(err, window.ErrWindowNotFound) || stdout.Len() != 0 {
					t.Fatalf("missing match: err=%v stdout=%q", err, stdout.String())
				}
			} else if err != nil || !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("zero interval wait: err=%v stdout=%q", err, stdout.String())
			}
		})
	}
}

func TestScreenWatchZeroPollUsesBoundedDefault(t *testing.T) {
	sc := &pftest.Screenshotter{Frames: []image.Image{pftest.SolidImage(2, 2, color.RGBA{A: 255})}}
	opens := 0
	cmd := screenCmd(func(context.Context) (*perfuncted.Session, error) { opens++; return pftest.New(sc, nil, nil, nil), nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"watch", "--rect=0,0,2,2", "--poll=0s", "--duration=15ms", "--output=json"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opens != 1 || !strings.Contains(stdout.String(), `"event":"initial"`) {
		t.Fatalf("zero poll watch: opens=%d output=%q", opens, stdout.String())
	}
}

func TestScreenshotTerminalPreflightPropagatesClosedFileStatError(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "closed-screenshot-output"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := refuseBinaryTerminal(file); err == nil || !strings.Contains(err.Error(), "inspect screenshot output") {
		t.Fatalf("closed screenshot file accepted: %v", err)
	}
}
