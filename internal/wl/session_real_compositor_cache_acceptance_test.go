package wl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func writeSessionEvent(conn *net.UnixConn, sender, opcode uint32, body []byte) error {
	pkt := make([]byte, 8+len(body))
	PutUint32(pkt[0:], sender)
	PutUint32(pkt[4:], uint32(len(pkt))<<16|opcode)
	copy(pkt[8:], body)
	_, err := conn.Write(pkt)
	return err
}
func privateSessionWaylandPeer(conn *net.UnixConn) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var registryID uint32
	syncCount := 0
	for {
		var header [8]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("private compositor read: %w", err)
		}
		sender, encoded := Uint32(header[:4]), Uint32(header[4:])
		size := int(encoded >> 16)
		if size < 8 || size > 65535 {
			return fmt.Errorf("malformed Wayland request length %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		switch {
		case sender == 1 && encoded&0xffff == 1:
			if len(body) != 4 {
				return fmt.Errorf("get_registry malformed %x", body)
			}
			registryID = Uint32(body)
		case sender == 1 && encoded&0xffff == 0:
			cb := Uint32(body)
			syncCount++
			if syncCount == 1 {
				for _, ev := range []struct {
					name    uint32
					iface   string
					version uint32
				}{
					{32, "wl_output", 4}, {15, "wl_seat", 7}, {50, "wl_output", 2},
				} {
					if err := writeSessionEvent(conn, registryID, 0, registryGlobalData(ev.name, ev.iface, ev.version)); err != nil {
						return err
					}
				}
			} else if syncCount == 2 {
				payload := make([]byte, 4)
				PutUint32(payload, 32)
				if err := writeSessionEvent(conn, registryID, 1, payload); err != nil {
					return err
				}
			}
			payload := make([]byte, 4)
			if err := writeSessionEvent(conn, cb, 0, payload); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unrecognized Wayland wire sender=%d opcode=%d", sender, encoded&0xffff)
		}
	}
}
func TestRealSyntheticWaylandSessionCacheRefcountsGlobalChangesAndContextCancellation(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "wl.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			serverDone <- err
			return
		}
		serverDone <- privateSessionWaylandPeer(conn)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first, err := NewSessionContext(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	expected := []GlobalEvent{{Name: 15, Interface: "wl_seat", Version: 7}, {Name: 32, Interface: "wl_output", Version: 4}, {Name: 50, Interface: "wl_output", Version: 2}}
	initial := first.GlobalsSnapshot()
	if len(initial) != len(expected) {
		t.Fatalf("registry populated %+v instead of %+v", initial, expected)
	}
	for i, e := range expected {
		if initial[i] != e {
			t.Fatalf("global[%d]=%+v want %+v", i, initial[i], e)
		}
	}
	second, err := NewSessionContext(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first.Ctx != second.Ctx {
		t.Fatal("cached Wayland sessions must share transport but not close handles")
	}
	initial[0].Interface = "untrusted mutation"
	if first.GlobalsSnapshot()[0].Interface == "untrusted mutation" {
		t.Fatal("global snapshot leaked mutable backing slice")
	}
	if err := second.Sync(); err != nil {
		t.Fatal(err)
	}
	got := first.GlobalsSnapshot()
	if len(got) != 2 || got[0].Name != 15 || got[1].Name != 50 {
		t.Fatalf("Wayland registry removal not propagated to shared handles: %+v", got)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if acquired, err := NewSessionContext(canceled, socket); !errors.Is(err, context.Canceled) || acquired != nil {
		t.Fatalf("canceled cache hit acquired live session: %v %v", acquired, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.SyncContext(ctx); err != nil {
		t.Fatalf("first close terminated second live handle: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.SyncContext(ctx); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("final close allowed sync: %v", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("private compositor shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("final reference did not close Wayland transport")
	}
}
func TestWaylandGlobalsSortStableUnderConcurrentIndependentSnapshots(t *testing.T) {
	s := &Session{globals: []GlobalEvent{{Name: 5, Interface: "wl_output", Version: 3}, {Name: 5, Interface: "wl_output", Version: 2}, {Name: 1, Interface: "wl_seat", Version: 1}}}
	sortGlobals(s.globals)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				got := s.GlobalsSnapshot()
				if len(got) != 3 || got[0].Name != 1 || got[1].Version != 2 || got[2].Version != 3 {
					t.Errorf("wrong stable snapshot %+v", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}
