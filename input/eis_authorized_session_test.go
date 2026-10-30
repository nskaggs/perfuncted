package input

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

// TestEICompleteAuthorizedSession exercises protocol negotiation, device grants,
// event framing, mutation and revocation using a real Unix socket and a local
// synthetic EIS peer. No compositor or user desktop is involved.
func TestEICompleteAuthorizedSession(t *testing.T) { //nolint:gocyclo // Full protocol or mock state transitions are asserted in one transaction.
	server, ctx, cancel, done := startEISenderWithResult(t)
	defer cancel()
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	const connID = uint64(0xff00000000000010)
	const seatID = uint64(0xff00000000000011)
	const devID = uint64(0xff00000000000012)
	// A single resumed device carries the mandatory pointer, button and keyboard
	// interfaces. Its desktop region has a negative-free origin and explicit bounds.
	stream := eiRequest(0, 2, append(append(eiU32(9), eiU64(connID)...), eiU32(1)...))
	stream = append(stream, eiRequest(connID, 1, eiU64(seatID))...)
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard", "ei_scroll", "ei_text"} {
		stream = append(stream, eiRequest(seatID, 2, append(eiU64(uint64(1<<i)), eiString(name)...))...)
	}
	stream = append(stream, eiRequest(seatID, 3, nil)...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	bind := readEIRequest(t, server)
	if bind.object != seatID || bind.opcode != 1 || len(bind.body) != 8 || binary.NativeEndian.Uint64(bind.body) != 31 {
		t.Fatalf("seat bind = %+v, want all granted capabilities", bind)
	}
	callback := readEIRequest(t, server)
	if callback.object != connID || callback.opcode != 0 || len(callback.body) < 8 {
		t.Fatalf("unexpected callback: %+v", callback)
	}
	callbackID := binary.NativeEndian.Uint64(callback.body[:8])

	stream = eiRequest(seatID, 4, append(eiU64(devID), eiU32(1)...))
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard", "ei_scroll", "ei_text"} {
		stream = append(stream, eiRequest(devID, 5, append(eiU64(101+uint64(i)), eiString(name)...))...)
	}
	region := append(append(append(append(eiU32(0), eiU32(0)...), eiU32(1920)...), eiU32(1080)...), eiF32(1)...)
	stream = append(stream, eiRequest(devID, 4, region)...)
	stream = append(stream, eiRequest(devID, 6, nil)...)
	stream = append(stream, eiRequest(devID, 7, eiU32(9))...)
	stream = append(stream, eiRequest(callbackID, 0, eiU64(0))...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	var sender *eiSender
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("setupEISender: %v", result.err)
		}
		sender = result.sender
	case <-ctx.Done():
		t.Fatalf("EIS negotiation timed out: %v", ctx.Err())
	}
	defer sender.client.close()
	start := readEIRequest(t, server)
	if start.object != devID || start.opcode != 1 {
		t.Fatalf("missing emulation start: %+v", start)
	}
	if !sender.validatePoint(100, 200) || sender.validatePoint(1920, 1080) {
		t.Fatal("authorized EIS region was not enforced")
	}

	portal := &portalSession{}
	backend := newRemoteDesktopBackend(sender, portal)
	defer backend.Close()
	if err := backend.KeyDown(ctx, "a"); err != nil {
		t.Fatalf("press authorized key: %v", err)
	}
	key := readEIRequest(t, server)
	frame := readEIRequest(t, server)
	if key.object != 103 || key.opcode != 1 || frame.object != devID || frame.opcode != 3 {
		t.Fatalf("key press not routed through granted interface: %+v, %+v", key, frame)
	}
	if err := backend.MouseMove(ctx, 100, 200); err != nil {
		t.Fatalf("move authorized pointer: %v", err)
	}
	pointer := readEIRequest(t, server)
	frame = readEIRequest(t, server)
	if pointer.object != 101 || pointer.opcode != 1 || frame.object != devID {
		t.Fatalf("pointer event: %+v, %+v", pointer, frame)
	}
	if err := backend.MouseMove(ctx, 1920, 1080); err == nil {
		t.Fatal("allowed input outside authorized region")
	}
	if err := backend.MouseDown(ctx, 1); err != nil {
		t.Fatalf("mouse down: %v", err)
	}
	button := readEIRequest(t, server)
	frame = readEIRequest(t, server)
	if button.object != 102 || button.opcode != 1 || frame.object != devID {
		t.Fatalf("button event: %+v, %+v", button, frame)
	}
	// A paused authorized device revokes all subsequent input operations.
	if _, err := server.Write(eiRequest(devID, 8, eiU32(10))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.revoked:
		if err := backend.KeyUp(ctx, "a"); !errors.Is(err, ErrRemoteDesktopRevoked) {
			t.Fatalf("post-revocation key up error: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("device revocation not observed: %v", ctx.Err())
	}
}

type eisSetupResult struct {
	sender *eiSender
	err    error
}

func startEISenderWithResult(t *testing.T) (*net.UnixConn, context.Context, context.CancelFunc, <-chan eisSetupResult) {
	t.Helper()
	client, server := unixConnectionPair(t)
	t.Cleanup(func() { _ = server.Close() })
	if err := server.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	done := make(chan eisSetupResult, 1)
	go func() { sender, err := setupEISender(ctx, client); done <- eisSetupResult{sender: sender, err: err} }()
	return server, ctx, cancel, done
}
