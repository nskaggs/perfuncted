package wl

import (
	"testing"
)

func TestNewSession_CacheHit(t *testing.T) {
	// Clear the cache first.
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	sock := "wayland-test-cache"
	fakeSess := &Session{Sock: sock, Ctx: &Context{}}
	sessionCacheMu.Lock()
	sessionCache[sock] = &sessionRef{sess: fakeSess, refs: 1}
	sessionCacheMu.Unlock()

	// NewSession should return the cached session and increment refcount.
	s, err := NewSession(sock)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if s != fakeSess {
		t.Fatal("NewSession did not return cached session")
	}
	if s.Sock != sock {
		t.Errorf("sock = %q, want %q", s.Sock, sock)
	}

	// Refcount should now be 2.
	sessionCacheMu.Lock()
	ref := sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref == nil {
		t.Fatal("session not in cache")
	}
	if ref.refs != 2 {
		t.Errorf("refcount = %d, want 2", ref.refs)
	}

	// Close should decrement but not destroy.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	sessionCacheMu.Lock()
	ref = sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref == nil {
		t.Fatal("session should still be in cache")
	}
	if ref.refs != 1 {
		t.Errorf("refcount after close = %d, want 1", ref.refs)
	}
}

func TestNewSession_CloseDecrementsRefcount(t *testing.T) {
	// Clear the cache.
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	sock := "wayland-test-refcount"
	fakeSess := &Session{Sock: sock, Ctx: &Context{}}
	sessionCacheMu.Lock()
	sessionCache[sock] = &sessionRef{sess: fakeSess, refs: 1}
	sessionCacheMu.Unlock()

	// Get two references.
	s1, err := NewSession(sock)
	if err != nil {
		t.Fatalf("NewSession 1: %v", err)
	}
	s2, err := NewSession(sock)
	if err != nil {
		t.Fatalf("NewSession 2: %v", err)
	}
	if s1 != s2 {
		t.Fatal("s1 and s2 should be the same session")
	}

	// Refcount should be 3 (initial 1 + 2 calls).
	sessionCacheMu.Lock()
	ref := sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref.refs != 3 {
		t.Errorf("refcount = %d, want 3", ref.refs)
	}

	// Close s1 → refcount 2.
	if err := s1.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}
	sessionCacheMu.Lock()
	ref = sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref == nil || ref.refs != 2 {
		t.Errorf("refcount after close 1 = %d, want 2", ref.refs)
	}

	// Close s2 → refcount 1.
	if err := s2.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}
	sessionCacheMu.Lock()
	ref = sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref == nil || ref.refs != 1 {
		t.Errorf("refcount after close 2 = %d, want 1", ref.refs)
	}

	// Session should still be in cache (refcount > 0).
	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if !exists {
		t.Error("session should still be in cache while refcount > 0")
	}
}

func TestNewSession_NotInCache(t *testing.T) {
	// Clear the cache.
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	// Try to get a session for a non-existent socket.
	sock := "/nonexistent/test.sock"
	_, err := NewSession(sock)
	if err == nil {
		t.Fatal("expected error for non-existent socket")
	}
	t.Logf("got expected error: %v", err)

	// Cache should be empty (failed connection not cached).
	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if exists {
		t.Error("failed session should not be cached")
	}
}

func TestNewSession_CacheMissCreatesNew(t *testing.T) {
	// Clear the cache.
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	// NewSession for an unreachable socket should fail but also
	// should not pollute the cache.
	sock := "/nonexistent/new.sock"
	_, err := NewSession(sock)
	if err == nil {
		t.Fatal("expected error")
	}
	// The session should have been created then closed (ctx.Close() path).
	// Since the connect failed, the session won't be in the cache.
	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if exists {
		t.Error("failed session should not remain in cache")
	}
}
