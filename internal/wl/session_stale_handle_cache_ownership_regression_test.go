package wl

import (
	"context"
	"errors"
	"net"
	"testing"
)

// A delayed Close from an old connection must never decrement or evict the
// replacement connection under the same compositor socket cache key.
func TestWaylandStaleSessionHandleCannotReleaseReplacementCacheOwner(t *testing.T) {
	sock := "synthetic-reused-compositor-cache-key"
	oldCanonical := &Session{Sock: sock, Display: &Display{}}
	oldRef := &sessionRef{sess: oldCanonical, refs: 1}
	currentCanonical := &Session{Sock: sock}
	currentRef := &sessionRef{sess: currentCanonical, refs: 2}
	sessionCacheMu.Lock()
	if sessionCache[sock] != nil {
		sessionCacheMu.Unlock()
		t.Fatal("test key in use")
	}
	sessionCache[sock] = currentRef
	sessionCacheMu.Unlock()
	t.Cleanup(func() {
		sessionCacheMu.Lock()
		if sessionCache[sock] == currentRef {
			delete(sessionCache, sock)
		}
		sessionCacheMu.Unlock()
	})
	stale := newSessionHandle(oldRef)
	if err := stale.Close(); err != nil {
		t.Fatalf("stale close: %v", err)
	}
	if err := stale.Close(); err != nil {
		t.Fatalf("stale double close: %v", err)
	}
	if err := stale.SyncContext(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("stale handle became usable: %v", err)
	}
	sessionCacheMu.Lock()
	got := sessionCache[sock]
	count := currentRef.refs
	sessionCacheMu.Unlock()
	if got != currentRef || count != 2 {
		t.Fatalf("stale close damaged replacement: owner=%p want=%p refs=%d", got, currentRef, count)
	}
	a, b := newSessionHandle(currentRef), newSessionHandle(currentRef)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	sessionCacheMu.Lock()
	got = sessionCache[sock]
	count = currentRef.refs
	sessionCacheMu.Unlock()
	if got != currentRef || count != 1 {
		t.Fatalf("replacement unexpectedly retired on first close: owner=%p refs=%d", got, count)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if exists {
		t.Fatal("final owner close left cache entry")
	}
}
