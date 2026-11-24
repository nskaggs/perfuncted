package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/pftest"
)

type workflowTestInput struct {
	pftest.Inputter
	entered chan string
	proceed chan struct{}
	cleaned chan struct{}
}

func (i *workflowTestInput) Type(ctx context.Context, text string) error {
	i.entered <- text
	if text != "first" {
		return nil
	}
	defer close(i.cleaned)
	select {
	case <-i.proceed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func workflowReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("workflow test timed out")
		var zero T
		return zero
	}
}

func workflowAwaitState(t *testing.T, w *workflowLifecycle, want workflowState) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		_, state, changed := w.snapshot()
		if state == want {
			return
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("state %s, want %s", state, want)
		}
	}
}

func TestWorkflowRunnerLifecycle(t *testing.T) {
	for _, mode := range []string{"resume", "stop-paused", "stop-in-flight", "parent-cancel", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := newWorkflowLifecycle(parent)
			defer w.stop()
			inp := &workflowTestInput{entered: make(chan string, 3), proceed: make(chan struct{}), cleaned: make(chan struct{})}
			session := pftest.New(&pftest.Screenshotter{}, inp, &pftest.Manager{}, nil)
			defer session.Close()
			runner := &scriptRunner{ctx: parent, lifecycle: w, out: io.Discard, pf: session}
			done := make(chan error, 1)
			go func() { done <- runner.run(strings.NewReader("input type first\ninput type second\n")) }()
			if got := workflowReceive(t, inp.entered); got != "first" {
				t.Fatal(got)
			}
			id, state, _ := w.snapshot()
			if id == "" || state != workflowRunning {
				t.Fatalf("initial identity/state: %q %s", id, state)
			}
			switch mode {
			case "resume", "stop-paused":
				if !w.pause() {
					t.Fatal("pause refused")
				}
				workflowAwaitState(t, w, workflowPausing)
				close(inp.proceed)
				workflowAwaitState(t, w, workflowPaused)
				select {
				case got := <-inp.entered:
					t.Fatalf("dispatch while paused: %s", got)
				default:
				}
				if mode == "resume" {
					if !w.resume() {
						t.Fatal("resume refused")
					}
				} else if !w.stop() {
					t.Fatal("stop refused")
				}
			case "stop-in-flight":
				if !w.stop() {
					t.Fatal("stop refused")
				}
			case "parent-cancel":
				cancel()
			case "legacy":
				close(inp.proceed)
			}
			err := workflowReceive(t, done)
			workflowReceive(t, inp.cleaned)
			want := workflowStopped
			if mode == "legacy" || mode == "resume" {
				want = workflowCompleted
				if err != nil {
					t.Fatal(err)
				}
				if got := workflowReceive(t, inp.entered); got != "second" {
					t.Fatal(got)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("want cancellation, got %v", err)
			}
			gotID, gotState, _ := w.snapshot()
			if gotID != id || gotState != want {
				t.Fatalf("final identity/state: %q %s", gotID, gotState)
			}
			if w.resume() || w.pause() || w.stop() {
				t.Fatal("terminal state accepted control")
			}
			select {
			case got := <-inp.entered:
				t.Fatalf("unexpected dispatch: %s", got)
			default:
			}
		})
	}
}

func TestWorkflowFailureAndIdentity(t *testing.T) {
	w := newWorkflowLifecycle(context.Background())
	other := newWorkflowLifecycle(context.Background())
	defer other.stop()
	if w.id == other.id {
		t.Fatal("duplicate run identity")
	}
	runner := &scriptRunner{lifecycle: w}
	if err := runner.run(strings.NewReader("unknown\n")); err == nil {
		t.Fatal("expected script error")
	}
	workflowAwaitState(t, w, workflowFailed)
}

func TestWorkflowCommandLocalCancellationFails(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(err.Error(), func(t *testing.T) {
			w := newWorkflowLifecycle(context.Background())
			if got := w.finish(err); !errors.Is(got, err) {
				t.Fatalf("finish returned %v, want %v", got, err)
			}
			workflowAwaitState(t, w, workflowFailed)
		})
	}
}
