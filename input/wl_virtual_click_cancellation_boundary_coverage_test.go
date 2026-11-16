//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

type cancelAfterPointerFrameCtx struct {
	recordingCtx
	cancel context.CancelFunc
}

func (c *cancelAfterPointerFrameCtx) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.recordingCtx.WriteMsg(data, oob); err != nil {
		return err
	}
	if len(data) >= 8 && wl.Uint32(data[4:8])&0xffff == 4 {
		c.cancel()
	}
	return nil
}

func TestWlVirtualClickCancellationAfterMotionDoesNotPressButton(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &WlVirtualBackend{outW: 100, outH: 100, ptr: &wl.RawProxy{}}
	backend.ptr.SetID(7)
	wire := &cancelAfterPointerFrameCtx{cancel: cancel}
	err := backend.mouseClickEvents(ctx, wire, 20, 30, btnLeft)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("click after motion cancellation = %v, want context.Canceled", err)
	}
	if len(wire.msgs) != 2 {
		t.Fatalf("messages before canceled click = %d, want motion and frame only", len(wire.msgs))
	}
	for _, msg := range wire.msgs {
		if opcode := wl.Uint32(msg[4:8]) & 0xffff; opcode == 2 {
			t.Fatal("click sent a button event after cancellation")
		}
	}
}
