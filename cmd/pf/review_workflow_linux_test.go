package main

import (
	"github.com/nskaggs/perfuncted/v2/internal/sessionregistry"

	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/internal/workflow"
	"golang.org/x/sys/unix"
)

func TestReviewControlledWorkflowRejectsNonregularScript(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "script.fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	// O_RDWR keeps the FIFO open without blocking the test itself. The
	// controlled runner must reject it before opening a desktop or reading.
	fd, err := unix.Open(fifo, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	for _, path := range []string{fifo, "/dev/null"} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			id := "rv-" + workflow.New(context.Background(), "").ID()[:24]
			called := false
			cmd := runCmd(func(context.Context) (*perfuncted.Session, error) {
				called = true
				return nil, errors.New("unexpected session open")
			}, nil)
			cmd.SetArgs([]string{path, "--workflow-id", id})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := cmd.ExecuteContext(ctx)
			if root, e := sessionregistry.WorkflowRegistry(); e == nil {
				_ = sessionregistry.Recover(root, id)
				root.Close()
			}
			if called || err == nil || !strings.Contains(err.Error(), "regular") {
				t.Fatalf("opened session=%v; error=%v", called, err)
			}
		})
	}
}

func TestReviewControlledWorkflowScriptStatFailure(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "script-*.pf")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validateControlledScriptFile(file); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed script stat error=%v", err)
	}
}
