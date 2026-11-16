//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type keyboardCancelOnDoneCall struct {
	context.Context
	closeAt int
	done    chan struct{}
	mu      sync.Mutex
	calls   int
}

func newKeyboardCancelOnDoneCall(closeAt int) *keyboardCancelOnDoneCall {
	return &keyboardCancelOnDoneCall{Context: context.Background(), closeAt: closeAt, done: make(chan struct{})}
}

func (c *keyboardCancelOnDoneCall) Done() <-chan struct{} {
	c.mu.Lock()
	c.calls++
	if c.calls == c.closeAt {
		close(c.done)
	}
	c.mu.Unlock()
	return c.done
}

func (c *keyboardCancelOnDoneCall) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func TestNewWlKeyboardReportsCreateAndWarmupCancellationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cancelOnCall int
		requestCount int
		wantError    string
	}{
		{name: "create virtual keyboard", cancelOnCall: 5, requestCount: 2, wantError: "create virtual keyboard"},
		{name: "warmup keymap", cancelOnCall: 7, requestCount: 3, wantError: "keyboard warmup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, registry, peer := newKeyboardConstructorTransport(t)
			if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatalf("set constructor request deadline: %v", err)
			}

			keyboard, err := newWlKeyboard(newKeyboardCancelOnDoneCall(tc.cancelOnCall), ctx, registry, 12, 1, 13)
			if keyboard != nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("canceled constructor request returned keyboard=%v err=%v", keyboard, err)
			}
			for i := 0; i < tc.requestCount; i++ {
				sender, opcode, _ := readKeyboardConstructorRequest(t, peer)
				if sender == 0 || opcode != 0 {
					t.Fatalf("constructor request %d = sender %d opcode %d", i, sender, opcode)
				}
			}
		})
	}
}
