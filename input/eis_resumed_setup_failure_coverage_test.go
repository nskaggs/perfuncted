//go:build linux

package input

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

func TestEISenderRejectsMalformedConnectionAnnouncementSize(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "empty body", body: nil},
		{name: "serial without connection identity", body: eiU32(7)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx, cancel, result := startEISenderWithResult(t)
			defer cancel()
			if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
				t.Fatalf("send handshake version: %v", err)
			}
			assertEISenderAnnouncesPingPong(t, server)
			if _, err := server.Write(eiRequest(0, 2, tc.body)); err != nil {
				t.Fatalf("send malformed connection announcement: %v", err)
			}
			select {
			case setup := <-result:
				wantSize := "size 0"
				if len(tc.body) != 0 {
					wantSize = "size 4"
				}
				if setup.sender != nil || setup.err == nil || !strings.Contains(setup.err.Error(), "invalid handshake connection event "+wantSize) {
					t.Fatalf("malformed connection announcement returned sender=%v err=%v", setup.sender, setup.err)
				}
			case <-ctx.Done():
				t.Fatalf("setup did not reject malformed connection announcement: %v", ctx.Err())
			}
		})
	}
}

type eisDeviceResumeWait struct {
	server     *net.UnixConn
	ctx        *eiObservedWaitContext
	result     <-chan error
	connection uint64
	seat       uint64
	callback   uint64
}

func prepareEISenderAtDeviceResumeWait(t *testing.T) eisDeviceResumeWait {
	t.Helper()
	server, ctx, _, result := startObservedEISenderSetup(t)
	awaitEISetupWait(t, ctx, 1)
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatalf("send handshake version: %v", err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	awaitEISetupWait(t, ctx, 2)
	const connection = uint64(0xff00000000000010)
	const seat = uint64(0xff00000000000020)
	assertEISenderAnswersPing(t, server, connection, eiServerIDFloor+0x31)
	awaitEISetupWait(t, ctx, 3)
	if _, err := server.Write(eiRequest(connection, 1, eiU64(seat))); err != nil {
		t.Fatalf("send seat identity: %v", err)
	}
	awaitEISetupWait(t, ctx, 4)
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
		capability := append(eiU64(uint64(1<<i)), eiString(name)...)
		if _, err := server.Write(eiRequest(seat, 2, capability)); err != nil {
			t.Fatalf("send %s seat capability: %v", name, err)
		}
	}
	if _, err := server.Write(eiRequest(seat, 3, nil)); err != nil {
		t.Fatalf("finish seat capabilities: %v", err)
	}
	bind := readEIRequest(t, server)
	if bind.object != seat || bind.opcode != 1 {
		t.Fatalf("seat binding request = %+v", bind)
	}
	synchronize := readEIRequest(t, server)
	if synchronize.object != connection || synchronize.opcode != 0 || len(synchronize.body) != 12 {
		t.Fatalf("device synchronization request = %+v", synchronize)
	}
	callback := binary.NativeEndian.Uint64(synchronize.body[:8])
	awaitEISetupWait(t, ctx, 5)
	return eisDeviceResumeWait{
		server: server, ctx: ctx, result: result,
		connection: connection, seat: seat, callback: callback,
	}
}

func TestEISenderRejectsMalformedEventWhileAwaitingResumedDevices(t *testing.T) {
	wait := prepareEISenderAtDeviceResumeWait(t)
	callbackDone := eiRequest(wait.callback, 0, eiU64(0))
	if _, err := wait.server.Write(callbackDone); err != nil {
		t.Fatalf("complete device synchronization: %v", err)
	}
	awaitEISetupWait(t, wait.ctx, 6)
	malformedDevice := eiRequest(wait.seat, 4, eiU64(eiServerIDFloor+0x41))
	if _, err := wait.server.Write(malformedDevice); err != nil {
		t.Fatalf("send malformed device event: %v", err)
	}
	select {
	case err := <-wait.result:
		if err == nil || !strings.Contains(err.Error(), "truncated") {
			t.Fatalf("malformed resumed device event error = %v", err)
		}
	case <-wait.ctx.Done():
		t.Fatalf("setup did not reject malformed resumed device event: %v", wait.ctx.Err())
	}
}

func TestRemoteDesktopRejectsPointerCoordinateNotRepresentableByEIS(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	backend.sender.pointers = []eiPointerDevice{{
		device: 701, pointer: 702,
		regions: []eiRegion{{x: 0, y: 0, width: 1<<24 + 4, height: 100, scale: 1}},
	}}

	err := backend.MouseMove(context.Background(), 1<<24+1, 10)
	if err == nil || !strings.Contains(err.Error(), "cannot be represented exactly") {
		t.Fatalf("inexact EIS coordinate error = %v", err)
	}
	assertNoEIWireRequest(t, peer)
	if backend.hasPointer {
		t.Fatal("unrepresentable pointer coordinate changed recorded location")
	}
	if err := backend.stateError(); err != nil {
		t.Fatalf("coordinate validation error retired the session: %v", err)
	}
}
