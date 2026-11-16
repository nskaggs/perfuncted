//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

type cancelAfterVirtualButtonFrame struct {
	recordingCtx
	cancel     context.CancelFunc
	buttonDown bool
}

func (c *cancelAfterVirtualButtonFrame) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.recordingCtx.WriteMsg(data, oob); err != nil {
		return err
	}
	if len(data) < 8 || wl.Uint32(data[:4]) != 7 {
		return nil
	}
	switch wl.Uint32(data[4:8]) & 0xffff {
	case 2:
		if len(data) >= 20 && wl.Uint32(data[16:20]) == 1 {
			c.buttonDown = true
		}
	case 4:
		if c.buttonDown {
			c.buttonDown = false
			c.cancel()
		}
	}
	return nil
}

func TestWlVirtualClickCancellationDuringHoldReleasesButton(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wire := &cancelAfterVirtualButtonFrame{cancel: cancel}
	backend := &WlVirtualBackend{outW: 100, outH: 100, ptr: &wl.RawProxy{}}
	backend.ptr.SetID(7)
	err := backend.mouseClickEvents(ctx, wire, 20, 30, btnLeft)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("click cancellation error = %v, want context.Canceled", err)
	}

	var buttonStates []uint32
	buttonUpIndex := -1
	for index, message := range wire.msgs {
		if wl.Uint32(message[4:8])&0xffff != 2 {
			continue
		}
		state := wl.Uint32(message[16:20])
		buttonStates = append(buttonStates, state)
		if state == 0 {
			buttonUpIndex = index
		}
	}
	if len(buttonStates) != 2 || buttonStates[0] != 1 || buttonStates[1] != 0 {
		t.Fatalf("button wire states = %v, want press followed by compensating release", buttonStates)
	}
	if buttonUpIndex < 0 || buttonUpIndex+1 >= len(wire.msgs) || wl.Uint32(wire.msgs[buttonUpIndex+1][4:8])&0xffff != 4 {
		t.Fatalf("button release was not followed by its frame: messages=%d release=%d", len(wire.msgs), buttonUpIndex)
	}
}

func TestWlVirtualMouseMoveRejectsCoordinatesAboveProtocolRange(t *testing.T) {
	coordinate := int64(^uint32(0)) + 1
	if int64(int(coordinate)) != coordinate {
		t.Skip("int cannot represent a coordinate above the Wayland uint32 range")
	}
	backend, peer := newCapturedWlVirtualBackend(t)
	err := backend.MouseMove(context.Background(), int(coordinate), 0)
	if err == nil || !strings.Contains(err.Error(), "exceed uint32 range") {
		t.Fatalf("oversized absolute coordinate returned %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if messages := readCapturedWlWrites(t, peer); len(messages) != 0 {
		t.Fatalf("invalid coordinate sent %d Wayland messages", len(messages))
	}
}

func TestWlVirtualMouseMovePropagatesTransportFailureWithoutFrame(t *testing.T) {
	wire := &failingCtx{successWrites: 0}
	backend := &WlVirtualBackend{outW: 100, outH: 100, ptr: &wl.RawProxy{}}
	backend.ptr.SetID(7)
	err := backend.mouseMoveEvent(context.Background(), wire, 20, 30)
	if err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("motion transport failure returned %v", err)
	}
	if len(wire.msgs) != 0 {
		t.Fatalf("failed motion sent %d Wayland messages", len(wire.msgs))
	}
}
