package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

type eisDequeueBoundaryContext struct {
	parent        context.Context
	onSecondCheck func()
	checks        int
}

func (c *eisDequeueBoundaryContext) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (c *eisDequeueBoundaryContext) Done() <-chan struct{}       { return c.parent.Done() }
func (c *eisDequeueBoundaryContext) Value(key any) any           { return c.parent.Value(key) }
func (c *eisDequeueBoundaryContext) Err() error {
	c.checks++
	if c.checks == 2 && c.onSecondCheck != nil {
		c.onSecondCheck()
	}
	return c.parent.Err()
}

// A queued EIS event may be selected at exactly the moment the caller loses
// authority. Model the transition at the second cancellation check to make the
// race deterministic: the client must not publish that event or advance serial.
func TestEISNextRejectsCancellationImmediatelyAfterDequeuingEvent(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &eisDequeueBoundaryContext{parent: parent, onSecondCheck: cancel}
	c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
	c.serial.Store(73)
	c.messages <- eiMessage{object: eiServerIDFloor + 9, opcode: 7, body: eiU32(999)}
	event, err := c.next(ctx)
	if !errors.Is(err, context.Canceled) || event.object != 0 || event.opcode != 0 || len(event.body) != 0 {
		t.Fatalf("canceled receive returned event=%+v err=%v", event, err)
	}
	if got := c.serial.Load(); got != 73 {
		t.Fatalf("revoked event advanced serial to %d", got)
	}
	if ctx.checks != 2 {
		t.Fatalf("unexpected cancellation checks: %d", ctx.checks)
	}
}

// The socket can terminate after the first preflight but before the selected
// event has been delivered. The post-dequeue terminal check must take priority
// over a buffered frame, even if the caller's own context is still active.
func TestEISNextRejectsTransportFailureImmediatelyAfterDequeuingEvent(t *testing.T) {
	failure := errors.New("synthetic peer revoked during dequeue")
	c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
	c.serial.Store(73)
	c.messages <- eiMessage{object: eiServerIDFloor + 9, opcode: 7, body: eiU32(999)}
	ctx := &eisDequeueBoundaryContext{parent: context.Background(), onSecondCheck: func() { c.setReadError(failure); close(c.done) }}
	event, err := c.next(ctx)
	if !errors.Is(err, failure) || event.object != 0 || event.opcode != 0 || len(event.body) != 0 {
		t.Fatalf("terminal receive returned event=%+v err=%v", event, err)
	}
	if got := c.serial.Load(); got != 73 {
		t.Fatalf("revoked event advanced serial to %d", got)
	}
	if ctx.checks != 2 {
		t.Fatalf("unexpected cancellation checks: %d", ctx.checks)
	}
}
