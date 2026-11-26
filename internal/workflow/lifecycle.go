// Package workflow owns the workflow execution lifecycle.
package workflow

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"

	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
)

// State is the stable serialized lifecycle state.
type State string

// States describe the existing workflow control protocol.
const (
	Running   State = "running"
	Pausing   State = "pause-requested"
	Paused    State = "paused"
	Resuming  State = "resuming"
	Stopping  State = "stopping"
	Stopped   State = "stopped"
	Failed    State = "failed"
	Completed State = "completed"
)

// Lifecycle belongs to one runner. The checkpoint is the dispatch
// linearization point: a command admitted before pause/stop is in flight.
type Lifecycle struct {
	id                string
	ctx               context.Context //nolint:containedctx // owns the run cancellation context
	cancel            context.CancelFunc
	mu                sync.Mutex
	state             State
	changed           chan struct{}
	lastCompletedStep int
	currentStep       int
	currentStepPhase  string
	terminalError     string
}

// Status is the bounded, privacy-safe snapshot of a workflow.
type Status struct {
	ID                string `json:"id"`
	State             State  `json:"state"`
	LastCompletedStep int    `json:"last_completed_step"`
	CurrentStep       int    `json:"current_step,omitempty"`
	CurrentStepPhase  string `json:"current_step_phase,omitempty"`
	TerminalError     string `json:"terminal_error,omitempty"`
}

// New creates an independent lifecycle. An empty id selects a random identity.
func New(parent context.Context, id string) *Lifecycle {
	if id == "" {
		id = rand.Text()
	}
	parent = contextutil.Default(parent)
	ctx, cancel := context.WithCancel(parent)
	return &Lifecycle{id: id, ctx: ctx, cancel: cancel, state: Running, changed: make(chan struct{})}
}

// Snapshot observes the identity, state and next-transition signal.
func (w *Lifecycle) Snapshot() (string, State, <-chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.id, w.state, w.changed
}

// Status returns a consistent snapshot.
func (w *Lifecycle) Status() Status {
	status, _ := w.StatusAndChange()
	return status
}

// StatusAndChange observes status and its next-transition signal under one lock.
func (w *Lifecycle) StatusAndChange() (Status, <-chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return Status{
		ID:                w.id,
		State:             w.state,
		LastCompletedStep: w.lastCompletedStep,
		CurrentStep:       w.currentStep,
		CurrentStepPhase:  w.currentStepPhase,
		TerminalError:     w.terminalError,
	}, w.changed
}

// BeginStep records a step and phase without dispatching work.
func (w *Lifecycle) BeginStep(step int, phase string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.currentStep = step
	w.currentStepPhase = phase
}

// CompleteStep records successful step completion.
func (w *Lifecycle) CompleteStep(step int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastCompletedStep = step
	w.currentStep = 0
	w.currentStepPhase = ""
}

func workflowErrorCategory(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "operation_failed"
	}
}

func (w *Lifecycle) transition(state State) {
	w.state = state
	close(w.changed)
	w.changed = make(chan struct{})
}

// Pause requests a pause at the next dispatch checkpoint.
func (w *Lifecycle) Pause() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ctx.Err() != nil {
		return false
	}
	switch w.state {
	case Running, Resuming:
		w.transition(Pausing)
	case Pausing, Paused:
	default:
		return false
	}
	return true
}

// Resume requests continued execution.
func (w *Lifecycle) Resume() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ctx.Err() != nil {
		return false
	}
	switch w.state {
	case Pausing, Paused:
		w.transition(Resuming)
	case Running, Resuming:
	default:
		return false
	}
	return true
}

// Stop requests cancellation unless the run is terminal.
func (w *Lifecycle) Stop() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch w.state {
	case Stopped, Failed, Completed:
		return false
	case Stopping:
		return true
	default:
		w.transition(Stopping)
		w.cancel()
		return true
	}
}

// Checkpoint waits for permission to dispatch the next step.
func (w *Lifecycle) Checkpoint() error {
	for {
		w.mu.Lock()
		if err := w.ctx.Err(); err != nil {
			w.mu.Unlock()
			return err
		}
		switch w.state {
		case Running:
			w.mu.Unlock()
			return nil
		case Resuming:
			w.transition(Running)
			w.mu.Unlock()
			return nil
		case Pausing:
			w.transition(Paused)
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

// Finish runs only after the interpreter and its synchronous command cleanup
// return. Stop wins over completion if it was requested before this boundary.
func (w *Lifecycle) Finish(err error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	defer w.cancel()
	switch {
	case w.state == Stopping || w.ctx.Err() != nil:
		w.transition(Stopped)
		if err == nil {
			err = w.ctx.Err()
		}
		if errors.Is(err, context.Canceled) && w.currentStep != 0 {
			w.currentStepPhase = "canceled"
		}
	case err != nil:
		w.transition(Failed)
	default:
		w.transition(Completed)
	}
	if w.state != Stopped || (err != nil && !errors.Is(err, context.Canceled)) {
		w.terminalError = workflowErrorCategory(err)
	}
	if w.state == Completed {
		w.currentStep = 0
		w.currentStepPhase = ""
	}
	return err
}

// ID returns the immutable run identity.
func (w *Lifecycle) ID() string { return w.id }

// Context returns the context canceled when the run stops.
func (w *Lifecycle) Context() context.Context { return w.ctx }

// Cancel releases the run's cancellation context.
func (w *Lifecycle) Cancel() { w.cancel() }
