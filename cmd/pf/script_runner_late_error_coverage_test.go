package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type scriptFailingSourceReader struct{ failure error }

func (r scriptFailingSourceReader) Read([]byte) (int, error) { return 0, r.failure }

// The script parser must propagate a real reader failure instead of treating
// it like a clean EOF or silently accepting an incomplete instruction.
func TestScriptRunPropagatesSourceReadFailure(t *testing.T) {
	failure := errors.New("synthetic script transport failed")
	runner := &scriptRunner{ctx: context.Background(), out: io.Discard}
	if err := runner.run(scriptFailingSourceReader{failure: failure}); !errors.Is(err, failure) {
		t.Fatalf("source failure lost: %v", err)
	}
}

// Multiword typing is a single literal input operation, and malformed grab
// coordinates are rejected before any capture destination can be created.
func TestScriptRunnerMultiwordInputAndInvalidGrabPath(t *testing.T) {
	runner, backend, _, _ := newScriptFixture(t)
	if err := runner.run(strings.NewReader("input type alpha beta gamma\n")); err != nil {
		t.Fatal(err)
	}
	if len(backend.Calls) != 1 || backend.Calls[0] != "type:alpha beta gamma" {
		t.Fatalf("input tokens were not joined: %v", backend.Calls)
	}
	if err := runner.execScreen(2, []string{"grab", "--rect", "invalid", "--out", t.TempDir() + "/must-not-exist.png"}); err == nil {
		t.Fatal("invalid grab bounds accepted")
	}
}

// Once a window has been selected, a stale target must not report a successful
// close when the compositor refuses the command.
func TestScriptSelectedWindowClosePropagatesRejection(t *testing.T) {
	runner, _, manager, _ := newScriptFixture(t)
	selected, err := findWindowByTitle(context.Background(), runner.pf.Windows, "Editor")
	if err != nil {
		t.Fatal(err)
	}
	runner.selectedWindow = selected
	failure := errors.New("synthetic compositor rejected close")
	manager.Err = failure
	if err := runner.runWindowAction("close", selected); !errors.Is(err, failure) {
		t.Fatalf("rejected window close returned %v", err)
	}
}
