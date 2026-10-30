package input

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"
)

func newAuthorizedEISOperationFixture(t *testing.T) (*RemoteDesktopBackend, *net.UnixConn) {
	t.Helper()
	client, server := unixConnectionPair(t)
	t.Cleanup(func() { _ = server.Close() })
	if err := server.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveSyntheticAuthorizedEISPeer(server) }()
	sender, err := setupEISender(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-peerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatalf("peer didn't finish protocol setup: %v", ctx.Err())
	}
	backend := newRemoteDesktopBackend(sender, &portalSession{})
	t.Cleanup(func() { _ = backend.Close() })
	return backend, server
}

func assertSyntheticEISOperation(t *testing.T, server *net.UnixConn, wantObject uint64, wantOpcode uint32) {
	t.Helper()
	event, err := readSyntheticEISFrame(server)
	if err != nil {
		t.Fatal(err)
	}
	if event.object != wantObject || event.opcode != wantOpcode {
		t.Fatalf("unexpected EIS request = %+v want object %d opcode %d", event, wantObject, wantOpcode)
	}
	frame, err := readSyntheticEISFrame(server)
	if err != nil {
		t.Fatal(err)
	}
	if frame.object != 0xff00000000000012 || frame.opcode != 3 {
		t.Fatalf("missing device frame after operation: %+v", frame)
	}
}

func TestEISAuthorizedInputExercisesTextMouseAndScroll(t *testing.T) { //nolint:gocyclo // Full protocol or mock state transitions are asserted in one transaction.
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx := context.Background()
	if err := backend.TypeLiteral(ctx, "é hello"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 105, 2)
	if err := backend.MouseMove(ctx, 300, 400); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 101, 1)
	if err := backend.MouseClick(ctx, 300, 400, 1); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 101, 1)
	assertSyntheticEISOperation(t, server, 102, 1)
	assertSyntheticEISOperation(t, server, 102, 1)
	if err := backend.ScrollUp(ctx, 1); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 104, 2)
	if err := backend.ScrollDown(ctx, 2); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 104, 2)
	if err := backend.ScrollLeft(ctx, 3); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 104, 2)
	if err := backend.ScrollRight(ctx, 4); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 104, 2)
	if err := backend.MouseDown(ctx, 1); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 102, 1)
	if err := backend.MouseUp(ctx, 1); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 102, 1)
	if err := backend.KeyDown(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 103, 1)
	if err := backend.KeyUp(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 103, 1)
	_, _, err := backend.PointerLocation(ctx)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("pointer query should declare unsupported: %v", err)
	}
	if len(backend.Diagnostics()) < 2 {
		t.Fatal("missing authorized backend diagnostics")
	}
	coord, err := backend.PointerCoordinateSpace(ctx)
	if err != nil || coord.ScaleX != 1 || coord.ScaleY != 1 {
		t.Fatalf("coordinate space %+v, %v", coord, err)
	}
}

func TestEISAuthorizedCloseReleasesHeldInput(t *testing.T) { //nolint:gocyclo // Full protocol or mock state transitions are asserted in one transaction.
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx := context.Background()
	if err := backend.KeyDown(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 103, 1)
	if err := backend.MouseDown(ctx, 1); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 102, 1)
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	key, err := readSyntheticEISFrame(server)
	if err != nil {
		t.Fatal(err)
	}
	if key.object != 103 || key.opcode != 1 || binary.NativeEndian.Uint32(key.body[4:8]) != 0 {
		t.Fatalf("held key not released during close: %+v", key)
	}
	frame, err := readSyntheticEISFrame(server)
	if err != nil || frame.object != 0xff00000000000012 {
		t.Fatalf("key release lacked frame: %+v %v", frame, err)
	}
	button, err := readSyntheticEISFrame(server)
	if err != nil {
		t.Fatal(err)
	}
	if button.object != 102 || button.opcode != 1 || binary.NativeEndian.Uint32(button.body[4:8]) != 0 {
		t.Fatalf("held button not released during close: %+v", button)
	}
	frame, err = readSyntheticEISFrame(server)
	if err != nil || frame.object != 0xff00000000000012 {
		t.Fatalf("button release lacked frame: %+v %v", frame, err)
	}
	stop, err := readSyntheticEISFrame(server)
	if err != nil || stop.object != 0xff00000000000012 || stop.opcode != 2 {
		t.Fatalf("device not stopped: %+v, %v", stop, err)
	}
}

func TestEISAuthorizedTypeValidatesFullSequenceBeforeSending(t *testing.T) { //nolint:gocyclo // Assert EIS request/response order and held modifier lifetime.
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx := context.Background()
	if err := backend.Type(ctx, "hello"); err != nil {
		t.Fatalf("literal text: %v", err)
	}
	assertSyntheticEISOperation(t, server, 105, 2)

	if err := backend.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("chord: %v", err)
	}
	want := []struct {
		key     uint32
		pressed uint32
	}{{29, 1}, {30, 1}, {30, 0}, {29, 0}}
	for i, expected := range want {
		request, err := readSyntheticEISFrame(server)
		if err != nil {
			t.Fatal(err)
		}
		if request.object != 103 || request.opcode != 1 || len(request.body) != 8 {
			t.Fatalf("chord transition %d incorrect EIS event: %+v", i, request)
		}
		gotKey := binary.NativeEndian.Uint32(request.body[:4])
		gotState := binary.NativeEndian.Uint32(request.body[4:8])
		if gotKey != expected.key || gotState != expected.pressed {
			t.Fatalf("chord transition %d = key %d state %d want %+v", i, gotKey, gotState, expected)
		}
		frame, err := readSyntheticEISFrame(server)
		if err != nil || frame.object != 0xff00000000000012 || frame.opcode != 3 {
			t.Fatalf("missing frame for chord transition %d: %+v %v", i, frame, err)
		}
	}
	if len(backend.heldKeys) != 0 {
		t.Fatalf("temporary chord keys were retained: %v", backend.heldKeys)
	}
	for _, invalid := range []string{"{ctrl+not-a-key}", string([]byte{0xff})} {
		if err := backend.Type(ctx, invalid); err == nil {
			t.Fatalf("accepted invalid key sequence %q", invalid)
		}
	}
	if len(backend.heldKeys) != 0 {
		t.Fatalf("invalid key sequence held keys: %v", backend.heldKeys)
	}
}

func TestEISAuthorizedTypePreservesExplicitlyHeldModifier(t *testing.T) {
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx := context.Background()
	if err := backend.KeyDown(ctx, "shift"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 103, 1)
	if err := backend.Type(ctx, "{shift+a}"); err != nil {
		t.Fatal(err)
	}
	for _, wantState := range []uint32{1, 0} {
		request, err := readSyntheticEISFrame(server)
		if err != nil {
			t.Fatal(err)
		}
		if request.object != 103 || binary.NativeEndian.Uint32(request.body[:4]) != 30 || binary.NativeEndian.Uint32(request.body[4:8]) != wantState {
			t.Fatalf("held shift produced unexpected key transition: %+v", request)
		}
		frame, err := readSyntheticEISFrame(server)
		if err != nil || frame.object != 0xff00000000000012 {
			t.Fatalf("missing keyboard frame: %+v %v", frame, err)
		}
	}
	if !backend.heldKeys[42] {
		t.Fatalf("explicitly held shift was accidentally released: %v", backend.heldKeys)
	}
	if err := backend.KeyUp(ctx, "shift"); err != nil {
		t.Fatal(err)
	}
	assertSyntheticEISOperation(t, server, 103, 1)
}
