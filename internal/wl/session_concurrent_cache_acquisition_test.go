package wl

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Simultaneous constructors can all finish a private Wayland registry
// handshake before any of them enters the session cache. Only one transport
// may remain owned, while each caller receives a distinct close handle.
func TestWaylandSessionConcurrentFirstAcquisitionSharesOneCachedTransport(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var peers sync.WaitGroup
	var peerMu sync.Mutex
	var peerErrors []error
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			peers.Add(1)
			go func() {
				defer peers.Done()
				if err := privateSessionWaylandPeer(conn); err != nil {
					peerMu.Lock()
					peerErrors = append(peerErrors, err)
					peerMu.Unlock()
				}
			}()
		}
	}()
	const n = 10
	start := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	type result struct {
		sess *Session
		err  error
	}
	results := make(chan result, n)
	for range n {
		go func() { <-start; sess, err := NewSessionContext(ctx, sock); results <- result{sess, err} }()
	}
	close(start)
	var handles []*Session
	for range n {
		res := <-results
		if res.err != nil || res.sess == nil {
			for _, h := range handles {
				_ = h.Close()
			}
			t.Fatalf("concurrent registry negotiation failed: session=%v err=%v", res.sess, res.err)
		}
		handles = append(handles, res.sess)
	}
	shared := handles[0].Ctx
	seenHandles := make(map[*Session]bool, len(handles))
	for i, h := range handles {
		if h.Ctx != shared {
			t.Fatalf("session handle %d did not share cached transport", i)
		}
		if seenHandles[h] {
			t.Fatalf("session handle %d reused another caller's close handle", i)
		}
		seenHandles[h] = true
	}
	sessionCacheMu.Lock()
	ref := sessionCache[sock]
	var count int
	if ref != nil {
		count = ref.refs
	}
	sessionCacheMu.Unlock()
	if count != n {
		t.Fatalf("shared session reference count=%d want %d", count, n)
	}
	for i := 0; i < n-1; i++ {
		if err := handles[i].Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := handles[n-1].SyncContext(ctx); err != nil {
		t.Fatalf("earlier closes terminated final handle: %v", err)
	}
	if err := handles[n-1].Close(); err != nil {
		t.Fatal(err)
	}
	sessionCacheMu.Lock()
	_, exists := sessionCache[sock]
	sessionCacheMu.Unlock()
	if exists {
		t.Fatal("final close retained stale cache entry")
	}
	if err := handles[n-1].SyncContext(ctx); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed handle sync=%v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-acceptDone:
	case <-time.After(time.Second):
		t.Fatal("accept loop did not exit")
	}
	finished := make(chan struct{})
	go func() { peers.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("losing Wayland connections leaked")
	}
	peerMu.Lock()
	defer peerMu.Unlock()
	if len(peerErrors) != 0 {
		t.Fatalf("private Wayland peers: %v", peerErrors)
	}
}
