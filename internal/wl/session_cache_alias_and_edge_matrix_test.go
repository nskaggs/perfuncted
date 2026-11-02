package wl

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync"
	"testing"
)

func TestWaylandGlobalCacheCopiesUpsertsAndRemovesAcrossSharedHandles(t *testing.T) {
	canonical := &Session{Sock: "synthetic-shared-registry"}
	ref := &sessionRef{sess: canonical, refs: 2}
	a, b := newSessionHandle(ref), newSessionHandle(ref)
	a.addGlobal(GlobalEvent{Name: 42, Interface: "wl_output", Version: 2})
	a.addGlobal(GlobalEvent{Name: 7, Interface: "wl_seat", Version: 1})
	a.addGlobal(GlobalEvent{Name: 42, Interface: "wl_output", Version: 4})
	want := []GlobalEvent{{Name: 7, Interface: "wl_seat", Version: 1}, {Name: 42, Interface: "wl_output", Version: 4}}
	if got := b.GlobalsSnapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("shared upsert replaced wrong global: got=%+v want=%+v", got, want)
	}
	observed := b.GlobalsSnapshot()
	observed[0].Interface = "untrusted modification"
	if got := a.GlobalsSnapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("caller mutation affected canonical registry: %+v", got)
	}
	b.removeGlobal(42)
	a.removeGlobal(99)
	if got := a.GlobalsSnapshot(); !reflect.DeepEqual(got, want[:1]) {
		t.Fatalf("global removal from shared handle lost unrelated entry: %+v", got)
	}
	b.removeGlobal(7)
	if got := a.GlobalsSnapshot(); len(got) != 0 {
		t.Fatalf("registry remained after complete removal: %+v", got)
	}
}

func TestWaylandSessionNilAndUncachedPartialOperationsAreSafe(t *testing.T) {
	var nilSession *Session
	nilSession.addGlobal(GlobalEvent{Name: 1})
	nilSession.removeGlobal(1)
	if got := nilSession.GlobalsSnapshot(); len(got) != 0 {
		t.Fatalf("nil session exposed globals: %+v", got)
	}
	if err := nilSession.Close(); err != nil {
		t.Fatalf("nil close: %v", err)
	}
	if err := nilSession.SyncContext(context.Background()); err != nil {
		t.Fatalf("nil sync: %v", err)
	}
	partial := &Session{Sock: "synthetic-unacquired-runtime"}
	if err := partial.SyncContext(context.Background()); err != nil {
		t.Fatalf("partial sync: %v", err)
	}
	if err := partial.Close(); err != nil {
		t.Fatalf("partial close: %v", err)
	}
	if err := partial.Close(); err != nil {
		t.Fatalf("idempotent partial close: %v", err)
	}
	partial.Display = &Display{}
	if err := partial.SyncContext(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed partial session re-entered compositor: %v", err)
	}
}

func TestWaylandSessionFinalReferenceWithoutTransportReleasesCacheExactlyOnce(t *testing.T) {
	sock := "synthetic-final-null-context-no-transport"
	canonical := &Session{Sock: sock}
	ref := &sessionRef{sess: canonical, refs: 2}
	sessionCacheMu.Lock()
	if _, exists := sessionCache[sock]; exists {
		sessionCacheMu.Unlock()
		t.Fatal("synthetic key is already owned")
	}
	sessionCache[sock] = ref
	sessionCacheMu.Unlock()
	t.Cleanup(func() { sessionCacheMu.Lock(); delete(sessionCache, sock); sessionCacheMu.Unlock() })
	first := newSessionHandle(ref)
	second := newSessionHandle(ref)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("duplicate release: %v", err)
	}
	sessionCacheMu.Lock()
	active := sessionCache[sock]
	sessionCacheMu.Unlock()
	if active != ref || active.refs != 1 {
		t.Fatalf("first close released other holder: ref=%p count=%d", active, ref.refs)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	sessionCacheMu.Lock()
	_, stillCached := sessionCache[sock]
	sessionCacheMu.Unlock()
	if stillCached || ref.refs != 0 {
		t.Fatalf("final release leaked cached session: cached=%v refs=%d", stillCached, ref.refs)
	}
}

func TestWaylandGlobalsSnapshotConcurrentWithGlobalLifecycle(t *testing.T) {
	s := &Session{}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				s.addGlobal(GlobalEvent{Name: uint32(id + 1), Interface: "wl_output", Version: uint32(i + 1)})
				for _, global := range s.GlobalsSnapshot() {
					if global.Name == 0 || global.Version == 0 {
						t.Errorf("invalid global: %+v", global)
					}
				}
				if i%5 == 0 {
					s.removeGlobal(uint32(id + 1))
				}
			}
		}(worker)
	}
	wg.Wait()
	if len(s.GlobalsSnapshot()) > 8 {
		t.Fatal("duplicate global names introduced under concurrent updates")
	}
}
