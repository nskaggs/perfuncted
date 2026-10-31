package output

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Exercise the real Wayland registry and xdg-output negotiation rather than
// injecting events into a manually constructed listener. This private socket
// impersonates a compositor with one fractional-scale logical output.
func TestWaylandListerCompletesXDGManagerNegotiationAndReleasesObjects(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
		registry, outputID, managerID, xdgID := uint32(0), uint32(0), uint32(0), uint32(0)
		roundtrip := 0
		for {
			msg, err := readWaylandMessage(conn)
			if errors.Is(err, io.EOF) {
				done <- nil
				return
			}
			if err != nil {
				done <- err
				return
			}
			sender, opcode, body := wl.Uint32(msg[:4]), wl.Uint32(msg[4:8])&0xffff, msg[8:]
			switch {
			case sender == 1 && opcode == 1:
				registry = wl.Uint32(body)
			case sender == registry && opcode == 0:
				name, newID, err := parseWaylandBind(body)
				if err != nil {
					done <- err
					return
				}
				switch name {
				case 4:
					outputID = newID
				case 8:
					managerID = newID
				default:
					done <- fmt.Errorf("unexpected global %d", name)
					return
				}
			case sender == managerID && managerID != 0 && opcode == 1:
				if len(body) != 8 {
					done <- fmt.Errorf("xdg_output request malformed: %x", body)
					return
				}
				xdgID = wl.Uint32(body[:4])
				if wl.Uint32(body[4:8]) != outputID {
					done <- fmt.Errorf("xdg output bound to wrong physical output")
					return
				}
			case sender == 1 && opcode == 0:
				roundtrip++
				switch roundtrip {
				case 1:
					for _, ev := range []struct {
						id      uint32
						iface   string
						version uint32
					}{{4, "wl_output", 4}, {8, "zxdg_output_manager_v1", 3}} {
						global := appendUint32(nil, ev.id)
						global = appendWlStringData(global, ev.iface)
						global = appendUint32(global, ev.version)
						if err = writeWaylandMessage(conn, registry, 0, global); err != nil {
							done <- err
							return
						}
					}
				case 2:
					if outputID == 0 || managerID == 0 {
						done <- fmt.Errorf("missing bound output or xdg manager")
						return
					}
					mode := make([]byte, 16)
					wl.PutUint32(mode[:4], wl.OutputModeCurrent)
					wl.PutUint32(mode[4:8], 2560)
					wl.PutUint32(mode[8:12], 1440)
					if err = writeWaylandMessage(conn, outputID, 1, mode); err != nil {
						done <- err
						return
					}
					if err = writeWaylandMessage(conn, outputID, 4, wlStringData("physical-DP")); err != nil {
						done <- err
						return
					}
				case 3:
					if xdgID == 0 {
						done <- fmt.Errorf("xdg output proxy not requested")
						return
					}
					pos := make([]byte, 8)
					wl.PutUint32(pos[:4], 100)
					wl.PutUint32(pos[4:8], 200)
					size := make([]byte, 8)
					wl.PutUint32(size[:4], 2048)
					wl.PutUint32(size[4:8], 1152)
					for _, ev := range []struct {
						op   uint32
						data []byte
					}{{0, pos}, {1, size}, {3, wlStringData("DP-1")}, {4, wlStringData("synthetic logical screen")}} {
						if err = writeWaylandMessage(conn, xdgID, ev.op, ev.data); err != nil {
							done <- err
							return
						}
					}
				}
				if len(body) < 4 {
					done <- fmt.Errorf("missing callback ID")
					return
				}
				if err = writeWaylandMessage(conn, wl.Uint32(body[:4]), 0, make([]byte, 4)); err != nil {
					done <- err
					return
				}
			case sender == xdgID && xdgID != 0 && opcode == 0:
				// zxdg_output_v1.destroy
			case sender == outputID && outputID != 0 && opcode == 0:
				// wl_output.release
			case sender == managerID && managerID != 0 && opcode == 0:
				// zxdg_output_manager_v1.destroy
			default:
				done <- fmt.Errorf("unexpected Wayland client message sender=%d opcode=%d body=%x", sender, opcode, body)
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	l, err := NewWaylandListerContext(ctx, sock)
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "DP-1" || got[0].Description != "synthetic logical screen" || got[0].Geometry.X != 100 || got[0].Geometry.Y != 200 || got[0].Geometry.W != 2048 || got[0].Geometry.H != 1152 || got[0].ScaleNumerator != 5 || got[0].ScaleDenominator != 4 {
		t.Fatalf("Wayland XDG manager negotiation returned %+v", got)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close negotiated Wayland backend: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Wayland connection not released")
	}
}
