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
