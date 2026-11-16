//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The first six checks bracket the handshake, connection, and seat messages.
const eisSeatCapabilityWaitErrCall = 7

// blockEISContextErr pauses setup at a known client.next boundary.
type blockEISContextErr struct {
	context.Context
	blockAt int
	calls   int
	reached chan struct{}
	release chan struct{}
}

func (c *blockEISContextErr) Err() error {
	c.calls++
	if c.calls == c.blockAt {
		close(c.reached)
		select {
		case <-c.release:
		case <-c.Context.Done():
		}
	}
	return c.Context.Err()
}

func TestSetupEISenderFailsWhenSeatCapabilityStreamEnds(t *testing.T) {
	clientConn, peer := unixConnectionPair(t)
	t.Cleanup(func() { _ = clientConn.Close() })
	t.Cleanup(func() { _ = peer.Close() })
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	baseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx := &blockEISContextErr{
		Context: baseCtx,
		blockAt: eisSeatCapabilityWaitErrCall,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() {
		_, err := setupEISender(ctx, clientConn)
		result <- err
	}()
	if _, err := peer.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatalf("send EIS handshake version: %v", err)
	}
	for index := 0; index < 14; index++ {
		request, err := readSyntheticEISFrame(peer)
		if err != nil {
			t.Fatalf("read sender setup request %d: %v", index+1, err)
		}
		if request.object != 0 {
			t.Fatalf("setup request %d targets object %d, want display object 0", index+1, request.object)
		}
		wantOpcode := uint32(4)
		switch index {
		case 0:
			wantOpcode = 0
		case 1:
			wantOpcode = 2
		case 2:
			wantOpcode = 3
		case 13:
			wantOpcode = 1
		}
		if request.opcode != wantOpcode {
			t.Fatalf("setup request %d opcode %d is out of handshake order", index+1, request.opcode)
		}
	}
	const connectionID = eiServerIDFloor + 0x10
	const seatID = eiServerIDFloor + 0x11
	connection := append(eiU32(7), eiU64(connectionID)...)
	connection = append(connection, eiU32(1)...)
	stream := append(eiRequest(0, 2, connection), eiRequest(connectionID, 1, eiU64(seatID))...)
	if _, err := peer.Write(stream); err != nil {
		t.Fatalf("send EIS connection and seat events: %v", err)
	}
	select {
	case <-ctx.reached:
	case <-baseCtx.Done():
		t.Fatalf("setup did not begin reading seat capabilities: %v", baseCtx.Err())
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	close(ctx.release)
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "read seat capabilities") {
			t.Fatalf("seat capability stream termination returned %v", err)
		}
	case <-baseCtx.Done():
		t.Fatalf("setup did not observe the closed seat capability stream: %v", baseCtx.Err())
	}
}
