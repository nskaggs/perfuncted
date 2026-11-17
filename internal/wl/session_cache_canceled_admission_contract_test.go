package wl

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type observedSessionCacheContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *observedSessionCacheContext) Err() error {
	c.once.Do(func() { close(c.checked) })
	return c.Context.Err()
}

func TestCanceledCallerDoesNotAcquireCachedWaylandSession(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "wayland.sock")
	ref := &sessionRef{sess: &Session{Sock: sock}, refs: 1}
	sessionCacheMu.Lock()
	if sessionCache == nil {
		sessionCache = make(map[string]*sessionRef)
	}
	if sessionCache[sock] != nil {
		sessionCacheMu.Unlock()
		t.Fatalf("unexpected session already cached at %q", sock)
	}
	sessionCache[sock] = ref
	locked := true
	defer func() {
		if locked {
			sessionCacheMu.Unlock()
		}
	}()
	t.Cleanup(func() {
		sessionCacheMu.Lock()
		if sessionCache[sock] == ref {
			delete(sessionCache, sock)
		}
		sessionCacheMu.Unlock()
	})

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedSessionCacheContext{Context: parent, checked: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		session, err := NewSessionContext(ctx, sock)
		if session != nil {
			_ = session.Close()
		}
		result <- err
	}()

	select {
	case <-ctx.checked:
	case <-time.After(time.Second):
		t.Fatal("session setup did not inspect the caller context before cache admission")
	}
	cancel()
	sessionCacheMu.Unlock()
	locked = false

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled cache admission returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled cache admission did not return")
	}

	sessionCacheMu.Lock()
	got, refs := sessionCache[sock], ref.refs
	if got == ref {
		delete(sessionCache, sock)
	}
	sessionCacheMu.Unlock()
	if got != ref || refs != 1 {
		t.Fatalf("canceled caller changed cache ownership: cached=%v refs=%d, want original session and one reference", got == ref, refs)
	}
}
