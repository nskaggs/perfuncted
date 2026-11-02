//go:build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/bendahl/uinput"
	"strings"
	"testing"
)

func TestUinputOrdinaryKeyOwnershipRejectsUnsolicitedReleaseAndRepeat(t *testing.T) {
	for _, key := range []string{"a", "enter", "f5"} {
		t.Run(key, func(t *testing.T) {
			b, kb := newTestBackend(t)
			if err := b.KeyUp(context.Background(), key); err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unowned release returned %v", err)
			}
			if len(kb.events) != 0 {
				t.Fatalf("unowned release injected events %v", kb.events)
			}
			if err := b.KeyDown(context.Background(), key); err != nil {
				t.Fatal(err)
			}
			if err := b.KeyDown(context.Background(), key); err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("duplicate down returned %v", err)
			}
			if len(kb.events) != 1 {
				t.Fatalf("duplicate down injected events %v", kb.events)
			}
			if err := b.KeyUp(context.Background(), key); err != nil {
				t.Fatal(err)
			}
			if len(kb.events) != 2 {
				t.Fatalf("owned release not dispatched once: %v", kb.events)
			}
			if err := b.KeyUp(context.Background(), key); err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("second unowned release returned %v", err)
			}
			if len(kb.events) != 2 {
				t.Fatalf("second unowned release injected events: %v", kb.events)
			}
		})
	}
}

func TestUinputOrdinaryKeyTypeBatchPreflightDoesNotInjectBeforeUnownedRelease(t *testing.T) {
	for _, seq := range []string{"sensitive{a up}", "{a down}{a down}", "{a down}{a up}{a up}"} {
		t.Run(seq, func(t *testing.T) {
			b, kb := newTestBackend(t)
			if err := b.Type(context.Background(), seq); err == nil {
				t.Fatalf("invalid held-state batch accepted %q", seq)
			}
			if len(kb.events) != 0 {
				t.Fatalf("rejected batch injected events %v", kb.events)
			}
		})
	}
	b, kb := newTestBackend(t)
	if err := b.Type(context.Background(), "{a down}{a up}hello"); err != nil {
		t.Fatalf("valid paired key action rejected: %v", err)
	}
	if len(kb.events) < 3 {
		t.Fatalf("valid batch sent too few events: %v", kb.events)
	}
}

func TestUinputHeldOrdinaryKeyCannotBeTappedByTextOrPartialBatch(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		literal       bool
		initial       bool
	}{
		{"literal held key", "a", true, true},
		{"typed held key after prefix", "secret a", false, true},
		{"same batch down then literal key", "{a down}a", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, kb := newTestBackend(t)
			if tc.initial {
				if err := b.KeyDown(context.Background(), "a"); err != nil {
					t.Fatal(err)
				}
			}
			count := len(kb.events)
			var err error
			if tc.literal {
				err = b.TypeLiteral(context.Background(), tc.command)
			} else {
				err = b.Type(context.Background(), tc.command)
			}
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("held ordinary key was pressed again: err=%v", err)
			}
			if len(kb.events) != count {
				t.Fatalf("denied text injected key events %v", kb.events[count:])
			}
		})
	}
}

func TestUinputOrdinaryKeyOwnershipSurvivesDeviceFailureAndRecovery(t *testing.T) {
	failure := errors.New("synthetic evdev write failure")
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("write_%d", failAt), func(t *testing.T) {
			kb := &syntheticFaultKeyboard{recordingKeyboard: &recordingKeyboard{}, failAt: failAt, failure: failure}
			b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
			if err := b.KeyDown(context.Background(), "a"); (failAt == 1) != errors.Is(err, failure) {
				t.Fatalf("down error = %v", err)
			}
			if failAt == 1 {
				if b.keyHeld(uinput.KeyA) {
					t.Fatal("failed down acquired ownership")
				}
				if err := b.KeyUp(context.Background(), "a"); err == nil {
					t.Fatal("failed-down key was released")
				}
				return
			}
			if !b.keyHeld(uinput.KeyA) {
				t.Fatal("successful keydown lost ownership")
			}
			if err := b.KeyUp(context.Background(), "a"); !errors.Is(err, failure) {
				t.Fatalf("up failure = %v", err)
			}
			if !b.keyHeld(uinput.KeyA) {
				t.Fatal("failed device release discarded ownership")
			}
			if err := b.KeyUp(context.Background(), "a"); err != nil {
				t.Fatalf("retry could not release held key: %v", err)
			}
			if b.keyHeld(uinput.KeyA) {
				t.Fatal("successful retry retained held state")
			}
		})
	}
}

func TestUinputOrdinaryKeyOwnershipSerializesConcurrentCalls(t *testing.T) {
	b, kb := newTestBackend(t)
	run := func(name string, fn func() error) {
		t.Helper()
		const count = 16
		var wg sync.WaitGroup
		results := make(chan error, count)
		for i := 0; i < count; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); results <- fn() }()
		}
		wg.Wait()
		close(results)
		successes := 0
		for err := range results {
			if err == nil {
				successes++
			} else if !strings.Contains(err.Error(), "held") {
				t.Fatalf("unexpected concurrent %s error: %v", name, err)
			}
		}
		if successes != 1 {
			t.Fatalf("%s admitted %d simultaneous owners, want 1", name, successes)
		}
	}
	run("press", func() error { return b.KeyDown(context.Background(), "a") })
	run("release", func() error { return b.KeyUp(context.Background(), "a") })
	if len(kb.events) != 2 {
		t.Fatalf("concurrent operations emitted %d events, want 2: %v", len(kb.events), kb.events)
	}
}
