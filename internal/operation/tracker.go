// Package operation owns admission, cancellation, and shutdown for backends
// whose individual operations can block on external protocol or device I/O.
package operation

import (
	"context"
	"sync"

	"github.com/nskaggs/perfuncted/internal/contextutil"
)

// Tracker is a zero-value-ready operation owner. One owner admits operations,
// cancels all admitted work on Close, then releases resources exactly once.
// Resource callbacks execute without holding the state mutex.
type Tracker struct {
	mu         sync.Mutex
	closed     bool
	closeDone  chan struct{}
	closeErr   error
	active     map[uint64]context.CancelFunc
	activeDone chan struct{}
	next       uint64
}

// Begin admits one cancellable operation and returns its idempotent release.
// It refuses new work as soon as shutdown begins.
func (t *Tracker) Begin(ctx context.Context, closedErr error) (context.Context, func(), error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	opCtx, cancel := context.WithCancel(ctx)
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		cancel()
		return nil, nil, closedErr
	}
	if t.active == nil {
		t.active = make(map[uint64]context.CancelFunc)
	}
	if len(t.active) == 0 {
		t.activeDone = make(chan struct{})
	}
	t.next++
	id := t.next
	t.active[id] = cancel
	t.mu.Unlock()

	var once sync.Once
	finish := func() {
		once.Do(func() {
			cancel()
			t.mu.Lock()
			delete(t.active, id)
			if len(t.active) == 0 {
				close(t.activeDone)
				t.activeDone = nil
			}
			t.mu.Unlock()
		})
	}
	return opCtx, finish, nil
}

// Closed reports whether shutdown has begun, including while resource cleanup
// or outstanding operation cancellation is still in progress.
func (t *Tracker) Closed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// Close rejects new admissions, cancels and drains existing operations, and
// runs release exactly once. Concurrent callers receive the same close error.
func (t *Tracker) Close(release func() error) error {
	t.mu.Lock()
	if t.closed {
		done := t.closeDone
		t.mu.Unlock()
		<-done
		t.mu.Lock()
		err := t.closeErr
		t.mu.Unlock()
		return err
	}
	t.closed = true
	t.closeDone = make(chan struct{})
	done := t.closeDone
	activeDone := t.activeDone
	cancels := make([]context.CancelFunc, 0, len(t.active))
	for _, cancel := range t.active {
		cancels = append(cancels, cancel)
	}
	t.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	if activeDone != nil {
		<-activeDone
	}
	var err error
	if release != nil {
		err = release()
	}
	t.mu.Lock()
	t.closeErr = err
	close(done)
	t.mu.Unlock()
	return err
}
