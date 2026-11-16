//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

type cancelAfterKeyboardMessage struct {
	recordingCtx
	cancel      context.CancelFunc
	matchOpcode uint32
	matchState  uint32
	matchKey    bool
	fired       bool
}

func (c *cancelAfterKeyboardMessage) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.recordingCtx.WriteMsg(data, oob); err != nil {
		return err
	}
	if len(data) < 8 || wl.Uint32(data[4:8])&0xffff != c.matchOpcode || c.fired {
		return nil
	}
	if c.matchKey && (len(data) < 20 || wl.Uint32(data[16:20]) != c.matchState) {
		return nil
	}
	c.fired = true
	c.cancel()
	return nil
}

func TestWlKeyboardSendkeysCancellationAfterKeymapDoesNotSendKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keyboard, _ := newSendkeysTestKeyboard()
	wire := &cancelAfterKeyboardMessage{cancel: cancel, matchOpcode: 0}
	keyboard.ctx = wire

	err := keyboard.sendkeys(ctx, []keySend{{text: "a"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sendkeys cancellation error = %v, want context.Canceled", err)
	}
	if len(wire.msgs) != 1 || wl.Uint32(wire.msgs[0][4:8])&0xffff != 0 {
		t.Fatalf("cancellation after keymap sent messages: %v", wire.msgs)
	}
}

func TestWlKeyboardSendkeysTextCancellationStopsAfterCurrentRune(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keyboard, _ := newSendkeysTestKeyboard()
	wire := &cancelAfterKeyboardMessage{cancel: cancel, matchOpcode: 1, matchState: 0, matchKey: true}
	keyboard.ctx = wire

	err := keyboard.sendkeys(ctx, []keySend{{text: "ab"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sendkeys cancellation error = %v, want context.Canceled", err)
	}
	var states []uint32
	for _, message := range wire.msgs {
		if len(message) >= 20 && wl.Uint32(message[4:8])&0xffff == 1 {
			states = append(states, wl.Uint32(message[16:20]))
		}
	}
	if len(states) != 2 || states[0] != 1 || states[1] != 0 {
		t.Fatalf("key states after cancellation = %v, want only current rune press and release", states)
	}
}
