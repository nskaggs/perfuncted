package main

import (
	"context"
	"crypto/rand"
	"sync"
)

type workflowState string

const (
	workflowRunning   workflowState = "running"
	workflowPausing   workflowState = "pause-requested"
	workflowPaused    workflowState = "paused"
	workflowResuming  workflowState = "resuming"
	workflowStopping  workflowState = "stopping"
	workflowStopped   workflowState = "stopped"
	workflowFailed    workflowState = "failed"
	workflowCompleted workflowState = "completed"
)

// workflowLifecycle belongs to one runner. The checkpoint is the dispatch
// linearization point: a command admitted before pause/stop is in flight.
type workflowLifecycle struct {
	id      string
	ctx     context.Context //nolint:containedctx // owns the run cancellation context
	cancel  context.CancelFunc
	mu      sync.Mutex
	state   workflowState
	changed chan struct{}
}

func newWorkflowLifecycle(parent context.Context) *workflowLifecycle {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &workflowLifecycle{id: rand.Text(), ctx: ctx, cancel: cancel, state: workflowRunning, changed: make(chan struct{})}
}

// snapshot also returns a notification channel closed on the next transition.
func (w *workflowLifecycle) snapshot() (string, workflowState, <-chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.id, w.state, w.changed
}

func (w *workflowLifecycle) transition(state workflowState) {
	w.state = state
	close(w.changed)
	w.changed = make(chan struct{})
}

func (w *workflowLifecycle) pause() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ctx.Err() != nil {
		return false
	}
	switch w.state {
	case workflowRunning, workflowResuming:
		w.transition(workflowPausing)
	case workflowPausing, workflowPaused:
	default:
		return false
	}
	return true
}

func (w *workflowLifecycle) resume() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ctx.Err() != nil {
		return false
	}
	switch w.state {
	case workflowPausing, workflowPaused:
		w.transition(workflowResuming)
	case workflowRunning, workflowResuming:
	default:
		return false
	}
	return true
}

func (w *workflowLifecycle) stop() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch w.state {
	case workflowStopped, workflowFailed, workflowCompleted:
		return false
	case workflowStopping:
		return true
	default:
		w.transition(workflowStopping)
		w.cancel()
		return true
	}
}

func (w *workflowLifecycle) checkpoint() error {
	for {
		w.mu.Lock()
		if err := w.ctx.Err(); err != nil {
			w.mu.Unlock()
			return err
		}
		switch w.state {
		case workflowRunning:
			w.mu.Unlock()
			return nil
		case workflowResuming:
			w.transition(workflowRunning)
			w.mu.Unlock()
			return nil
		case workflowPausing:
			w.transition(workflowPaused)
		}
		changed := w.changed
		w.mu.Unlock()
		select {
		case <-changed:
		case <-w.ctx.Done():
			return w.ctx.Err()
		}
	}
}

// finish runs only after the interpreter and its synchronous command cleanup
// return. Stop wins over completion if it was requested before this boundary.
func (w *workflowLifecycle) finish(err error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	defer w.cancel()
	switch {
	case w.state == workflowStopping || w.ctx.Err() != nil:
		w.transition(workflowStopped)
		if err == nil {
			err = w.ctx.Err()
		}
	case err != nil:
		w.transition(workflowFailed)
	default:
		w.transition(workflowCompleted)
	}
	return err
}
