package wl

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
)

func TestSessionGlobalsSnapshotPreservesRepeatedInterfacesAndRemoval(t *testing.T) {
	session := &Session{globals: make([]GlobalEvent, 0)}
	registry := &Registry{}
	registry.SetGlobalHandler(session.addGlobal)
	registry.SetGlobalRemoveHandler(session.removeGlobal)

	registry.Dispatch(0, -1, registryGlobalData(12, "wl_output", 3))
	registry.Dispatch(0, -1, registryGlobalData(4, "wl_output", 4))
	registry.Dispatch(0, -1, registryGlobalData(9, "wl_seat", 7))

	got := session.GlobalsSnapshot()
	want := []GlobalEvent{
		{Name: 4, Interface: "wl_output", Version: 4},
		{Name: 9, Interface: "wl_seat", Version: 7},
		{Name: 12, Interface: "wl_output", Version: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("globals length = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("global[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	registry.Dispatch(1, -1, []byte{4, 0, 0, 0})
	got = session.GlobalsSnapshot()
	if len(got) != 2 || got[0].Name != 9 || got[1].Name != 12 {
		t.Fatalf("globals after removal = %+v, want names 9, 12", got)
	}

	got[0].Interface = "mutated"
	if snapshot := session.GlobalsSnapshot(); snapshot[0].Interface == "mutated" {
		t.Fatal("GlobalsSnapshot returned session-owned storage")
	}
}

func registryGlobalData(name uint32, iface string, version uint32) []byte {
	data := make([]byte, 0, 8+len(iface)+8)
	var nameData [4]byte
	PutUint32(nameData[:], name)
	data = append(data, nameData[:]...)
	data = putStr(data, iface)
	var versionData [4]byte
	PutUint32(versionData[:], version)
	data = append(data, versionData[:]...)
	return data
}

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
	sessionCache[sock] = &sessionRef{
		sess: fakeSess,
		refs: 1,
	}
	sessionCacheMu.Unlock()

	// NewSession should return the cached session and increment refcount.
	s, err := NewSession(sock)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if s == fakeSess {
		t.Fatal("NewSession returned the cache owner instead of a close handle")
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
		return
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
		return
	}
	if ref.refs != 1 {
		t.Errorf("refcount after close = %d, want 1", ref.refs)
	}
}

func TestSessionHandleRejectsOperationsAfterClose(t *testing.T) {
	canonical := &Session{Ctx: &Context{}, Display: &Display{}}
	handle := &Session{
		Ctx:     canonical.Ctx,
		Display: canonical.Display,
		ref:     &sessionRef{sess: canonical, refs: 2},
	}

	if err := handle.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := handle.SyncContext(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed handle SyncContext error = %v, want %v", err, net.ErrClosed)
	}
	if err := canonical.SyncContext(context.Background()); err != nil {
		t.Fatalf("remaining session reference became unusable: %v", err)
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
	sessionCache[sock] = &sessionRef{
		sess: fakeSess,
		refs: 1,
	}
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
	if s1 == s2 {
		t.Fatal("session acquisitions should have distinct close handles")
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

func TestSessionCloseRemovesCacheAtZeroRefs(t *testing.T) {
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	sock := "wayland-test-zero"
	fakeSess := &Session{Sock: sock, Ctx: &Context{}}
	sessionCacheMu.Lock()
	sessionCache[sock] = &sessionRef{
		sess: fakeSess,
		refs: 1,
	}
	sessionCacheMu.Unlock()

	if err := fakeSess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if exists {
		t.Fatal("session remained in cache after final close")
	}
}

func TestNewSessionCacheConcurrentHitAndClose(t *testing.T) {
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()

	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	sock := "wayland-test-concurrent"
	fakeSess := &Session{Sock: sock, Ctx: &Context{}}
	sessionCacheMu.Lock()
	sessionCache[sock] = &sessionRef{
		sess: fakeSess,
		refs: 1,
	}
	sessionCacheMu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := NewSession(sock)
			if err != nil {
				t.Errorf("NewSession: %v", err)
				return
			}
			if err := s.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	wg.Wait()

	sessionCacheMu.Lock()
	ref := sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref == nil {
		t.Fatal("original cached session was removed")
		return
	}
	if ref.refs != 1 {
		t.Fatalf("refcount = %d, want 1", ref.refs)
	}
}

func TestSessionCloseDoesNotReleaseReplacementSession(t *testing.T) {
	sessionCacheMu.Lock()
	savedCache := sessionCache
	sessionCache = make(map[string]*sessionRef)
	sessionCacheMu.Unlock()
	defer func() {
		sessionCacheMu.Lock()
		sessionCache = savedCache
		sessionCacheMu.Unlock()
	}()

	sock := "wayland-test-replacement"
	first := &Session{Sock: sock, Ctx: &Context{}}
	firstRef := &sessionRef{sess: first, refs: 1}
	second := &Session{Sock: sock, Ctx: &Context{}}
	secondRef := &sessionRef{sess: second, refs: 1}
	sessionCacheMu.Lock()
	sessionCache[sock] = firstRef
	sessionCacheMu.Unlock()

	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	sessionCacheMu.Lock()
	sessionCache[sock] = secondRef
	sessionCacheMu.Unlock()

	if err := first.Close(); err != nil {
		t.Fatalf("repeated first Close: %v", err)
	}
	sessionCacheMu.Lock()
	ref := sessionCache[sock]
	sessionCacheMu.Unlock()
	if ref != secondRef || ref.refs != 1 {
		t.Fatalf("replacement session was modified: ref=%p refs=%d", ref, ref.refs)
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
