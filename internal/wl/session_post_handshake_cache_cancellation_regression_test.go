package wl

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// Cancellation during the final cache-admission lock must neither publish
// an unowned Wayland connection nor acquire an unrelated replacement owner.
func TestWaylandCancelWhileFinalSessionCacheAdmissionBlocked(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := "unowned"
		if replacement {
			name = "replacement"
		}
		t.Run(name, func(t *testing.T) {
			sock := shortPrivateSocketPath(t)
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ready := make(chan struct{})
			reply := make(chan struct{})
			sent := make(chan struct{})
			peerDone := make(chan error, 1)
			go func() {
				peer, err := listener.AcceptUnix()
				if err != nil {
					peerDone <- err
					return
				}
				defer peer.Close()
				_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
				var header [8]byte
				for {
					if _, err := io.ReadFull(peer, header[:]); err != nil {
						peerDone <- err
						return
					}
					n := int(Uint32(header[4:]) >> 16)
					if n < 8 || n > 65535 {
						peerDone <- errors.New("invalid compositor request")
						return
					}
					body := make([]byte, n-8)
					if _, err := io.ReadFull(peer, body); err != nil {
						peerDone <- err
						return
					}
					if Uint32(header[:4]) == 1 && Uint32(header[4:])&0xffff == 0 {
						callback := Uint32(body)
						close(ready)
						<-reply
						if err := writeSessionEvent(peer, callback, 0, make([]byte, 4)); err != nil {
							peerDone <- err
							return
						}
						close(sent)
						var one [1]byte
						_, err := peer.Read(one[:])
						peerDone <- err
						return
					}
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				s, err := NewSessionContext(ctx, sock)
				if s != nil {
					_ = s.Close()
				}
				done <- err
			}()
			select {
			case <-ready:
			case <-time.After(3 * time.Second):
				t.Fatal("synthetic compositor never reached registry sync")
			}
			sessionCacheMu.Lock()
			var owner *sessionRef
			if replacement {
				owner = &sessionRef{sess: &Session{Sock: sock}, refs: 1}
				sessionCache[sock] = owner
			}
			close(reply)
			select {
			case <-sent:
			case <-time.After(time.Second):
				sessionCacheMu.Unlock()
				t.Fatal("compositor never completed registry sync")
			}
			// The handshake reply is now available while cache admission is held.
			// Give the client time to reach the lock, then cancel before unlocking.
			time.Sleep(10 * time.Millisecond)
			cancel()
			sessionCacheMu.Unlock()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled cache acquisition: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("canceled cache acquisition hung")
			}
			sessionCacheMu.Lock()
			got, found := sessionCache[sock]
			if got == owner && owner != nil {
				delete(sessionCache, sock)
			}
			sessionCacheMu.Unlock()
			if replacement {
				if got != owner || owner.refs != 1 {
					t.Fatalf("replacement cache corrupted: got=%p owner=%p refs=%d", got, owner, owner.refs)
				}
			} else if found {
				t.Fatal("canceled uncached session published a connection")
			}
			select {
			case err := <-peerDone:
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					t.Fatalf("canceled handshake leaked connection: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("canceled Wayland connection not closed")
			}
		})
	}
}
