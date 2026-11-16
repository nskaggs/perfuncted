package input

import (
	"context"
	"errors"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestEISenderRejectsPeerClosedBeforeHandshake(t *testing.T) {
	client, peer := unixConnectionPair(t)
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sender, err := setupEISender(ctx, client)
	if sender != nil || err == nil || !errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "handshake event") {
		t.Fatalf("closed EIS peer returned sender=%v err=%v, want handshake EOF", sender, err)
	}
}

func TestEISenderReportsPeerWriteShutdownDuringHandshake(t *testing.T) {
	client, peer := unixConnectionPair(t)
	defer peer.Close()
	if _, err := peer.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	if err := peer.CloseRead(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sender, err := setupEISender(ctx, client)
	if sender != nil || err == nil || !strings.Contains(err.Error(), "configure sender") || !errors.Is(err, syscall.EPIPE) {
		t.Fatalf("closed EIS command path returned sender=%v err=%v", sender, err)
	}
}

func TestEISenderReportsPeerWriteShutdownWhileBindingSeat(t *testing.T) {
	server, ctx, cancel, result := startObservedEISenderSetup(t)
	defer cancel()
	awaitEISetupWait(t, ctx, 1)
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatalf("send handshake version: %v", err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	awaitEISetupWait(t, ctx, 2)

	const connectionID = uint64(0xff00000000000010)
	const pingID = uint64(0xff00000000000011)
	assertEISenderAnswersPing(t, server, connectionID, pingID)
	awaitEISetupWait(t, ctx, 3)

	const seatID = uint64(0xff00000000000020)
	if _, err := server.Write(eiRequest(connectionID, 1, eiU64(seatID))); err != nil {
		t.Fatalf("send seat identity: %v", err)
	}
	awaitEISetupWait(t, ctx, 4)
	for i, capability := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
		body := append(eiU64(1<<i), eiString(capability)...)
		if _, err := server.Write(eiRequest(seatID, 2, body)); err != nil {
			t.Fatalf("send %s seat capability: %v", capability, err)
		}
		awaitEISetupWait(t, ctx, uint32(5+i))
	}
	if err := server.CloseRead(); err != nil {
		t.Fatalf("shut down peer read side: %v", err)
	}
	if _, err := server.Write(eiRequest(seatID, 3, nil)); err != nil {
		t.Fatalf("finish seat capabilities: %v", err)
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "bind seat") || !errors.Is(err, syscall.EPIPE) {
			t.Fatalf("seat bind write failure = %v, want bind seat and EPIPE", err)
		}
	case <-ctx.Done():
		t.Fatalf("EIS setup did not report seat bind write failure: %v", ctx.Err())
	}
}
