//go:build linux

package input

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/wl"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestVirtualKeyboardCanceledEmptySequenceCannotAcknowledgeSuccess(t *testing.T) {
	keyboard, wire := newTestKeyboard()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := keyboard.sendkeys(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("already-canceled empty key sequence reported success: %v", err)
	}
	if len(wire.msgs) != 0 {
		t.Fatalf("canceled empty sequence wrote to compositor: %v", wire.msgs)
	}
}

func TestVirtualKeyboardRejectsStaleOrCollidingHeldSlotsBeforeCompositorMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		held map[string]uint32
		want string
	}{
		{"below reserved XKB base", map[string]uint32{"a": 7}, "invalid held slot"},
		{"above XKB range", map[string]uint32{"a": kcDynMax + 1}, "invalid held slot"},
		{"two held keys alias same slot", map[string]uint32{"a": kcDynBase, "b": kcDynBase}, "share slot"},
		{"invalid utf8 held key", map[string]uint32{string([]byte{0xff}): kcDynBase}, "invalid held character"},
		{"multi rune held key", map[string]uint32{"ab": kcDynBase}, "invalid held character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			k.held = tc.held
			if err := k.sendkeys(context.Background(), []keySend{{text: "safe"}}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("stale keyboard state not rejected: %v", err)
			}
			if len(wire.msgs) != 0 {
				t.Fatalf("stale key state sent compositor frames: %v", wire.msgs)
			}
		})
	}
}

func newKeyboardConstructorTransport(t *testing.T) (*wl.Context, *wl.Registry, *net.UnixConn) {
	t.Helper()
	socket := shortPrivateSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	peerReady := make(chan *net.UnixConn, 1)
	peerErr := make(chan error, 1)
	go func() {
		peer, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			peerErr <- acceptErr
			return
		}
		peerReady <- peer
	}()

	ctx, err := wl.Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctx.Close() })
	var peer *net.UnixConn
	select {
	case peer = <-peerReady:
	case err := <-peerErr:
		t.Fatalf("accept Wayland client: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	registry := &wl.Registry{}
	ctx.Register(registry)
	return ctx, registry, peer
}

func readKeyboardConstructorRequest(t *testing.T, peer *net.UnixConn) (uint32, uint32, []byte) {
	t.Helper()
	var header [8]byte
	if _, err := io.ReadFull(peer, header[:]); err != nil {
		t.Fatalf("read Wayland request header: %v", err)
	}
	word := binary.LittleEndian.Uint32(header[4:])
	size := int(word >> 16)
	if size < len(header) || size%4 != 0 {
		t.Fatalf("invalid Wayland request size %d", size)
	}
	body := make([]byte, size-len(header))
	if _, err := io.ReadFull(peer, body); err != nil {
		t.Fatalf("read Wayland request body: %v", err)
	}
	return binary.LittleEndian.Uint32(header[:4]), word & 0xffff, body
}

func TestNewWlKeyboardReturnsCanceledSeatBind(t *testing.T) {
	ctx, registry, _ := newKeyboardConstructorTransport(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	kbd, err := newWlKeyboard(canceled, ctx, registry, 12, 1, 13)
	if kbd != nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "bind wl_seat") {
		t.Fatalf("canceled seat bind returned keyboard=%v err=%v", kbd, err)
	}
}

func TestNewWlKeyboardRejectsZeroManagerVersionAfterSeatBind(t *testing.T) {
	ctx, registry, peer := newKeyboardConstructorTransport(t)
	kbd, err := newWlKeyboard(context.Background(), ctx, registry, 12, 0, 13)
	if kbd != nil || err == nil || !strings.Contains(err.Error(), "bind virtual keyboard manager") || !strings.Contains(err.Error(), "version=0") {
		t.Fatalf("zero manager version returned keyboard=%v err=%v", kbd, err)
	}

	sender, opcode, body := readKeyboardConstructorRequest(t, peer)
	if sender != registry.ID() || opcode != 0 || len(body) != 24 {
		t.Fatalf("first constructor request sender=%d opcode=%d body=%d bytes", sender, opcode, len(body))
	}
	if name := binary.LittleEndian.Uint32(body[:4]); name != 13 {
		t.Fatalf("seat bind targeted global %d, want 13", name)
	}
	if length := binary.LittleEndian.Uint32(body[4:8]); length != 8 || string(body[8:16]) != "wl_seat\x00" {
		t.Fatalf("seat bind interface length=%d name=%q, want wl_seat", length, body[8:16])
	}
	if version := binary.LittleEndian.Uint32(body[16:20]); version != 1 {
		t.Fatalf("seat bind version = %d, want 1", version)
	}
	if id := binary.LittleEndian.Uint32(body[20:24]); id == 0 {
		t.Fatal("seat bind used a zero client object id")
	}
}

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

func TestVirtualKeyboardDirectKeyLifecycleRejectsInvalidAndUnheldState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*wlKeyboard) error
		want   string
	}{
		{"multi character release", func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "many") }, "multi-character"},
		{"unheld named release", func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "enter") }, "not held"},
		{"unheld character release", func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "x") }, "not held"},
		{"multi character press", func(k *wlKeyboard) error { return k.pressKey(context.Background(), "multiple") }, "multi-character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			err := tc.invoke(k)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid key accepted: %v", err)
			}
			if len(wire.msgs) != 0 || len(k.held) != 0 || k.mods != 0 {
				t.Fatalf("invalid input crossed wire: messages=%d held=%v mods=%d", len(wire.msgs), k.held, k.mods)
			}
		})
	}
}

func TestVirtualKeyboardDirectKeyLifecyclePreservesHeldStateOnWriteFailures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		preHeld       string
		mods          uint32
		invoke        func(*wlKeyboard) error
		successWrites int
		want          string
	}{
		{"named press keymap", "", 0, func(k *wlKeyboard) error { return k.pressKey(context.Background(), "enter") }, 0, "write failure"},
		{"named press key", "", 0, func(k *wlKeyboard) error { return k.pressKey(context.Background(), "enter") }, 1, "write failure"},
		{"literal press keymap", "", 0, func(k *wlKeyboard) error { return k.pressKey(context.Background(), "é") }, 0, "write failure"},
		{"literal press key", "", 0, func(k *wlKeyboard) error { return k.pressKey(context.Background(), "é") }, 1, "write failure"},
		{"literal release keymap", "é", 0, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "é") }, 0, "write failure"},
		{"literal release key", "é", 0, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "é") }, 1, "write failure"},
		{"named release keymap", "enter", 0, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "enter") }, 0, "write failure"},
		{"named release key", "enter", 0, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "enter") }, 1, "write failure"},
		{"modifier release key", "", modControl, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "ctrl") }, 0, "write failure"},
		{"modifier release mask", "", modControl, func(k *wlKeyboard) error { return k.releaseKey(context.Background(), "ctrl") }, 1, "write failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _ := newTestKeyboard()
			if tc.preHeld != "" {
				if tc.preHeld == "enter" {
					k.held[tc.preHeld] = kcReturn
				} else {
					k.held[tc.preHeld] = kcDynBase
				}
			}
			k.mods = tc.mods
			peer := &failingCtx{successWrites: tc.successWrites}
			k.ctx = peer
			err := tc.invoke(k)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("write error lost: %v", err)
			}
			if k.mods != tc.mods {
				t.Fatalf("failed write incorrectly changed held modifiers: %d want %d", k.mods, tc.mods)
			}
			if tc.preHeld != "" && len(k.held) != 1 {
				t.Fatalf("failed release untracked key: %v", k.held)
			}
			if tc.preHeld == "" && len(k.held) != 0 {
				t.Fatalf("failed press tracked uncommitted key: %v", k.held)
			}
		})
	}
}

func TestVirtualKeyboardWarmupFailurePreservesCancellationAndTransportError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt int
	}{
		{"keymap", 0}, {"modifier down", 1}, {"modifier release", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _ := newTestKeyboard()
			peer := &failingCtx{successWrites: tc.failAt}
			k.ctx = peer
			err := k.warmupContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "write failure") {
				t.Fatalf("broken warmup accepted: %v", err)
			}
		})
	}
	k, wire := newTestKeyboard()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := k.warmupContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled warmup returned %v", err)
	}
	if wire.writes != 0 {
		t.Fatalf("canceled warmup touched compositor %d times", wire.writes)
	}
}

// Inject a transient failed write at every actual request in each of the
// major keyboard forms. A failed tap must leave no depressed key or temporary
// modifiers at either the compositor wire boundary or in the backend's state.
func TestVirtualKeyboardTransientFailureAtEveryWireStepReleasesKeys(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []keySend
	}{
		{"plain named tap", []keySend{{key: "enter"}}},
		{"literal text", []keySend{{text: "a"}}},
		{"named tap with ctrl", []keySend{{key: "enter", modifiers: modifiers{ctrl: true}}}},
		{"literal key with shift", []keySend{{key: "a", modifiers: modifiers{shift: true}}}},
		{"tap standalone modifier", []keySend{{key: "ctrl"}}},
		{"tap named with all modifiers", []keySend{{key: "f2", modifiers: modifiers{ctrl: true, alt: true, shift: true, super: true}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reference, _ := newTestKeyboard()
			refWire := &recordingCtx{}
			reference.ctx = refWire
			if err := reference.sendkeys(context.Background(), tc.actions); err != nil {
				t.Fatal(err)
			}
			if refWire.writes < 2 {
				t.Fatalf("test did not exercise a complete key transition: writes=%d", refWire.writes)
			}
			for failAt := 0; failAt < refWire.writes; failAt++ {
				k, _ := newTestKeyboard()
				wire := &failOnceKeyboardWrite{failAt: failAt}
				k.ctx = wire
				err := k.sendkeys(context.Background(), tc.actions)
				if err == nil || !strings.Contains(err.Error(), "injected one-time keyboard write failure") {
					t.Errorf("write %d/%d failure acknowledged: %v", failAt, refWire.writes, err)
					continue
				}
				down := map[uint32]bool{}
				var lastMods uint32
				for _, e := range keyboardEventsFromMessages(t, wire.msgs) {
					switch e.opcode {
					case 1:
						down[e.keycode] = e.state == 1
					case 2:
						lastMods = e.state
					}
				}
				for key, held := range down {
					if held {
						t.Errorf("failure at write %d/%d left keycode %d pressed: %+v", failAt, refWire.writes, key, wire.msgs)
					}
				}
				if lastMods != 0 || k.mods != 0 || len(k.held) != 0 {
					t.Errorf("failure at write %d/%d stranded modifiers: wire=%d mods=%d held=%v", failAt, refWire.writes, lastMods, k.mods, k.held)
				}
			}
		})
	}
}

// A failed virtual-keyboard keymap upload cannot be cached as accepted:
// retry must resend the keymap after transport recovery, while successful
// identical subsequent uploads must remain idempotent.
func TestVirtualKeyboardRetriesUnacknowledgedKeymapAfterTransportRecovery(t *testing.T) {
	keyboard, transport := newFailingKeyboard(0)
	const keymap = "xkb_keymap { xkb_keycodes { <K008> = 8; }; };"
	if err := keyboard.uploadKeymap(context.Background(), keymap); err == nil {
		t.Fatal("failed keymap upload was acknowledged")
	}
	if keyboard.lastKeymap != "" || transport.writes != 0 {
		t.Fatalf("failed keymap was cached: last=%q writes=%d", keyboard.lastKeymap, transport.writes)
	}
	transport.successWrites = 1
	if err := keyboard.uploadKeymap(context.Background(), keymap); err != nil {
		t.Fatalf("keymap upload did not recover: %v", err)
	}
	if keyboard.lastKeymap != keymap || transport.writes != 1 {
		t.Fatalf("recovered keymap was not cached: last=%q writes=%d", keyboard.lastKeymap, transport.writes)
	}
	if err := keyboard.uploadKeymap(context.Background(), keymap); err != nil || transport.writes != 1 {
		t.Fatalf("duplicate keymap transmission: err=%v writes=%d", err, transport.writes)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := keyboard.uploadKeymap(canceled, keymap+" changed"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled upload was acknowledged: %v", err)
	}
	if keyboard.lastKeymap != keymap || transport.writes != 1 {
		t.Fatal("canceled upload changed the accepted keymap")
	}
	// The accepted frame remains a normal Wayland keymap request.
	if got := wl.Uint32(transport.msgs[0][4:8]) & 0xffff; got != 0 {
		t.Fatalf("unexpected keymap request opcode: %d", got)
	}
}

type keyboardKeymapWire struct {
	recordingCtx
	maps []string
}

func (r *keyboardKeymapWire) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if len(data) >= 16 && wl.Uint32(data[4:8])&0xffff == 0 {
		cms, err := syscall.ParseSocketControlMessage(oob)
		if err != nil {
			return err
		}
		if len(cms) != 1 {
			return fmt.Errorf("expected keymap fd, got %d control messages", len(cms))
		}
		fds, err := syscall.ParseUnixRights(&cms[0])
		if err != nil {
			return err
		}
		if len(fds) != 1 {
			return fmt.Errorf("expected 1 keymap fd, got %d", len(fds))
		}
		dup, err := syscall.Dup(fds[0])
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(dup), "keymap test duplicate")
		// Ancillary FDs refer to a shared memory file whose writer is at EOF;
		// the compositor maps it from zero rather than reading that offset.
		if _, lintShadow45 := f.Seek(0, io.SeekStart); lintShadow45 != nil {
			_ = f.Close()
			return lintShadow45
		}
		lintShadow49, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			return err
		}
		r.maps = append(r.maps, string(lintShadow49))
	}
	return r.recordingCtx.WriteMsgContext(ctx, data, oob)
}

// Remapping an XKB code while that key is held can make releasing it target
// a different key, so a different batch of Unicode input must preserve the
// original held key's wire mapping across keymap uploads.
func TestVirtualKeyboardRetainsHeldKeymapIdentityAcrossInterleavedUnicode(t *testing.T) {
	for _, tc := range []struct{ name, held, text string }{
		{"named enter", "enter", "é"},
		{"named escape", "escape", "é"},
		{"literal unicode", "é", "Ω"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _ := newTestKeyboard()
			wire := &keyboardKeymapWire{}
			k.ctx = wire
			if err := k.sendkeys(context.Background(), []keySend{{key: tc.held, down: true}}); err != nil {
				t.Fatal(err)
			}
			heldSlot := k.held[tc.held]
			if heldSlot == 0 {
				t.Fatal("no initial held slot")
			}
			first := len(wire.msgs)
			if err := k.sendkeys(context.Background(), []keySend{{text: tc.text}, {key: tc.held, up: true}}); err != nil {
				t.Fatal(err)
			}
			events := keyboardEventsFromMessages(t, wire.msgs[first:])
			if len(events) != 3 || events[0].keycode == heldSlot || events[2].keycode != heldSlot || events[2].state != 0 {
				t.Fatalf("wrong held-key wire handling: slot=%d events=%+v", heldSlot, events)
			}
			if len(wire.maps) < 2 {
				t.Fatal("missing compositor keymap upload")
			}
			last := wire.maps[len(wire.maps)-1]
			slotTag := fmt.Sprintf("key <K%03d>", heldSlot)
			expectedSymbol := "Return"
			if tc.held == "escape" {
				expectedSymbol = "Escape"
			}
			if tc.held == "é" {
				expectedSymbol = "U00E9"
			}
			if !strings.Contains(last, slotTag+" { [ "+expectedSymbol+" ] }") {
				t.Fatalf("held %q was remapped before release: original slot=%d, keymap entry missing: %s", tc.held, heldSlot, last)
			}
		})
	}
}

func TestVirtualKeyboardRetainsTwoHeldKeysAcrossRepeatedKeymapUploads(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.sendkeys(context.Background(), []keySend{{key: "enter", down: true}, {key: "escape", down: true}, {key: "é", down: true}}); err != nil {
		t.Fatal(err)
	}
	slots := map[string]uint32{}
	for key, slot := range k.held {
		slots[key] = slot
	}
	if len(slots) != 3 {
		t.Fatalf("expected three independently held keys, got %v", slots)
	}
	initial := len(wire.msgs)
	if err := k.sendkeys(context.Background(), []keySend{{text: "Ω"}, {key: "enter", up: true}}); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs[initial:])
	if len(events) != 3 || events[0].keycode == slots["enter"] || events[0].keycode == slots["escape"] || events[0].keycode == slots["é"] || events[2].keycode != slots["enter"] || events[2].state != 0 {
		t.Fatalf("interleaved Unicode reassigned held key slot: %+v held=%v", events, slots)
	}
	if _, ok := k.held["enter"]; ok {
		t.Fatal("released Enter still marked held")
	}
	if k.held["escape"] != slots["escape"] || k.held["é"] != slots["é"] {
		t.Fatalf("remaining held keys remapped: %v", k.held)
	}
	second := len(wire.msgs)
	if err := k.sendkeys(context.Background(), []keySend{{key: "escape", up: true}, {key: "é", up: true}}); err != nil {
		t.Fatal(err)
	}
	events = keyboardEventsFromMessages(t, wire.msgs[second:])
	if len(events) != 2 || events[0].keycode != slots["escape"] || events[1].keycode != slots["é"] || events[0].state != 0 || events[1].state != 0 || len(k.held) != 0 {
		t.Fatalf("remaining held keys not released at original slots: events=%+v held=%v", events, k.held)
	}
}

func TestVirtualKeyboardCapacityFailureWithHeldSlotNeverWritesOrChangesState(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.sendkeys(context.Background(), []keySend{{key: "enter", down: true}}); err != nil {
		t.Fatal(err)
	}
	slot := k.held["enter"]
	before := len(wire.msgs)
	var input strings.Builder
	for i := 0; i < int(maxDynamicKeySlots); i++ {
		input.WriteRune(rune(0x1000 + i))
	}
	err := k.sendkeys(context.Background(), []keySend{{text: input.String()}})
	if err == nil || !strings.Contains(err.Error(), "dynamic slots") {
		t.Fatalf("full dynamic slot allocation overwrote held key: %v", err)
	}
	if len(wire.msgs) != before || k.held["enter"] != slot {
		t.Fatalf("capacity rejection mutated compositor or held key: messages=%d before=%d held=%v", len(wire.msgs), before, k.held)
	}
}

func TestVirtualKeyboardStaticHeldNamedKeySurvivesDynamicSequence(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.pressKey(context.Background(), "return"); err != nil {
		t.Fatal(err)
	}
	if k.held["return"] != kcReturn {
		t.Fatalf("static pressed key uses wrong slot: %v", k.held)
	}
	before := len(wire.msgs)
	if err := k.sendkeys(context.Background(), []keySend{{text: "é"}, {key: "return", up: true}}); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs[before:])
	if len(events) != 3 || events[2].state != 0 || events[2].keycode != kcReturn || len(k.held) != 0 {
		t.Fatalf("static key moved during keymap update: events=%+v held=%v", events, k.held)
	}
	last := wire.maps[len(wire.maps)-1]
	if !strings.Contains(last, "key <K012> { [ Return ] }") {
		t.Fatalf("static named key was omitted from later keymap: %s", last)
	}
}

func TestVirtualKeyboardDirectKeyDownDoesNotRemapAnotherHeldKey(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.pressKey(context.Background(), "é"); err != nil {
		t.Fatal(err)
	}
	heldSlot := k.held["é"]
	before := len(wire.msgs)
	if err := k.pressKey(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs[before:])
	if len(events) != 1 || events[0].state != 1 || events[0].keycode == heldSlot {
		t.Fatalf("second KeyDown reused active physical keycode: held slot=%d events=%+v", heldSlot, events)
	}
	if k.held["é"] != heldSlot || k.held["x"] == heldSlot {
		t.Fatalf("held keys alias after independent KeyDown calls: %v", k.held)
	}
	if err := k.releaseKey(context.Background(), "é"); err != nil {
		t.Fatal(err)
	}
	if err := k.releaseKey(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if len(k.held) != 0 {
		t.Fatalf("releases left held keys: %v", k.held)
	}
}

func TestVirtualKeyboardDirectKeyUpUsesOriginalSendkeysHeldSlot(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.sendkeys(context.Background(), []keySend{{key: "enter", down: true}}); err != nil {
		t.Fatal(err)
	}
	heldSlot := k.held["enter"]
	before := len(wire.msgs)
	if err := k.releaseKey(context.Background(), "enter"); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs[before:])
	if len(events) != 1 || events[0].keycode != heldSlot || events[0].state != 0 {
		t.Fatalf("KeyUp released a different XKB code: held=%d events=%+v", heldSlot, events)
	}
}

func TestVirtualKeyboardDirectKeyUpRestoresHeldModifierAndUnrelatedKey(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.pressKey(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	if err := k.pressKey(context.Background(), "é"); err != nil {
		t.Fatal(err)
	}
	if err := k.pressKey(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	xSlot := k.held["x"]
	initial := len(wire.msgs)
	if err := k.releaseKey(context.Background(), "é"); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs[initial:])
	if len(events) < 2 || events[0].opcode != 2 || events[0].state != modControl {
		t.Fatalf("key release did not preserve caller-held control modifier: %+v", events)
	}
	if !strings.Contains(wire.maps[len(wire.maps)-1], fmt.Sprintf("key <K%03d>", xSlot)) {
		t.Fatalf("unrelated held key remapped during release: held=%d map=%s", xSlot, wire.maps[len(wire.maps)-1])
	}
	if err := k.releaseKey(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if err := k.releaseKey(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	if len(k.held) != 0 || k.mods != 0 {
		t.Fatalf("release leaked held state: %v mods=%d", k.held, k.mods)
	}
}

// A Wayland virtual-keyboard tap always contains an up event. It must not
// consume an explicitly held key, including an earlier down in the same batch.
func TestVirtualKeyboardNeverTapsHeldKeyWithoutExplicitRelease(t *testing.T) {
	for _, tc := range []struct {
		name           string
		setup, actions []keySend
	}{
		{"prior held literal", []keySend{{key: "a", down: true}}, []keySend{{key: "a"}}},
		{"prior held named", []keySend{{key: "enter", down: true}}, []keySend{{key: "enter"}}},
		{"prior held modifier", []keySend{{key: "ctrl", down: true}}, []keySend{{key: "ctrl"}}},
		{"same batch literal", nil, []keySend{{key: "a", down: true}, {key: "a"}}},
		{"same batch named after text", nil, []keySend{{text: "private"}, {key: "enter", down: true}, {key: "enter"}}},
		{"same batch modifier", nil, []keySend{{key: "ctrl", down: true}, {key: "ctrl"}}},
		{"text taps held letter", []keySend{{key: "a", down: true}}, []keySend{{text: "a"}}},
		{"mixed text taps held letter", []keySend{{key: "a", down: true}}, []keySend{{text: "private a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			if len(tc.setup) > 0 {
				if err := k.sendkeys(context.Background(), tc.setup); err != nil {
					t.Fatalf("establish held key: %v", err)
				}
			}
			before := len(wire.msgs)
			priorMods := k.mods
			priorHeld := make(map[string]uint32, len(k.held))
			for name, slot := range k.held {
				priorHeld[name] = slot
			}
			err := k.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("tap consumed held key without rejecting batch: %v", err)
			}
			if len(wire.msgs) != before {
				t.Fatalf("invalid batch transmitted %d compositor messages", len(wire.msgs)-before)
			}
			if k.mods != priorMods || len(k.held) != len(priorHeld) {
				t.Fatalf("rejected batch modified holds: mods=%d expected=%d held=%v expected=%v", k.mods, priorMods, k.held, priorHeld)
			}
			for name, slot := range priorHeld {
				if k.held[name] != slot {
					t.Fatalf("held key %q moved to another slot: before=%d after=%d", name, slot, k.held[name])
				}
			}
		})
	}
}

func TestVirtualKeyboardNeverRepeatsHeldKeyDown(t *testing.T) {
	for _, tc := range []struct {
		name           string
		setup, actions []keySend
	}{
		{"prior held letter", []keySend{{key: "b", down: true}}, []keySend{{key: "b", down: true}}},
		{"prior held modifier", []keySend{{key: "shift", down: true}}, []keySend{{key: "shift", down: true}}},
		{"same batch letter", nil, []keySend{{key: "b", down: true}, {key: "b", down: true}}},
		{"same batch modifier", nil, []keySend{{key: "ctrl", down: true}, {key: "ctrl", down: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			if len(tc.setup) > 0 {
				if err := k.sendkeys(context.Background(), tc.setup); err != nil {
					t.Fatal(err)
				}
			}
			before := len(wire.msgs)
			err := k.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("repeated key down accepted: %v", err)
			}
			if len(wire.msgs) != before {
				t.Fatalf("repeated key down transmitted %d wire messages", len(wire.msgs)-before)
			}
		})
	}
}

// The virtual keyboard may receive literal text directly from TypeLiteral.
// Invalid UTF-8 must not silently become U+FFFD while sending a valid prefix.
func TestVirtualKeyboardRejectsInvalidUTF8BeforeSendingAnyPrefix(t *testing.T) {
	bad := string([]byte{0xff, 0xfe})
	for _, tc := range []struct {
		name    string
		actions []keySend
	}{
		{"literal text at start", []keySend{{text: bad}}},
		{"invalid trailing text", []keySend{{text: "valid"}, {text: bad}}},
		{"invalid nonnamed key", []keySend{{key: bad}}},
		{"invalid key after accelerator", []keySend{{key: "enter"}, {key: bad}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyboard, wire := newTestKeyboard()
			err := keyboard.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("malformed keyboard text accepted: %v", err)
			}
			if len(wire.msgs) != 0 || len(keyboard.held) != 0 || keyboard.mods != 0 {
				t.Fatalf("invalid input applied partial keyboard state: %d messages, mods=%d held=%v", len(wire.msgs), keyboard.mods, keyboard.held)
			}
		})
	}
}

// A virtual keyboard sends evdev key states only as 0 (released) or 1
// (pressed). Invalid states and below-base XKB codes cannot be forwarded to
// the compositor, even if subsequent frames would otherwise be valid.
func TestVirtualKeyboardRejectsInvalidKeyWireTransitionsBeforeEmission(t *testing.T) {
	k, wire := newTestKeyboard()
	for _, tc := range []struct {
		name           string
		keycode, state uint32
		want           string
	}{
		{"below XKB base", 7, 1, "below the XKB base"},
		{"invalid pressed state", kcReturn, 2, "invalid key state"},
		{"maximum uint32 state", kcReturn, ^uint32(0), "invalid key state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(wire.msgs)
			err := k.sendKey(context.Background(), tc.keycode, tc.state)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid protocol transition accepted: %v", err)
			}
			if len(wire.msgs) != before {
				t.Fatalf("invalid key transition sent %d events", len(wire.msgs)-before)
			}
		})
	}
	if err := k.sendKey(context.Background(), kcReturn, 1); err != nil {
		t.Fatalf("valid key-down rejected: %v", err)
	}
	if err := k.sendKey(context.Background(), kcReturn, 0); err != nil {
		t.Fatalf("valid key-up rejected: %v", err)
	}
	if len(wire.msgs) != 2 {
		t.Fatalf("expected exactly two valid transitions, got %d", len(wire.msgs))
	}
	for i, msg := range wire.msgs {
		if len(msg) != 20 || wl.Uint32(msg[4:8])&0xffff != 1 || wl.Uint32(msg[12:16]) != kcReturn-8 || wl.Uint32(msg[16:20]) != uint32(1-i) {
			t.Fatalf("invalid transmitted key event %d: %x", i, msg)
		}
	}
}

// A virtual keyboard must not acknowledge or cache a keymap if the process
// cannot allocate its private shared-memory descriptor. Use an isolated
// subprocess because the descriptor limit is process-wide.
func TestVirtualKeyboardRejectsKeymapWhenDescriptorQuotaExhausted(t *testing.T) {
	const child = "PF_TEST_KEYBOARD_NO_FDS"
	if os.Getenv(child) == "1" {
		k, transport := newTestKeyboard()
		const keymap = "xkb_keymap { xkb_keycodes { <K008> = 8; }; };"
		var original syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &original); err != nil {
			t.Fatal(err)
		}
		limited := original
		limited.Cur = 0
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limited); err != nil {
			t.Fatal(err)
		}
		err := k.uploadKeymap(context.Background(), keymap)
		if restoreErr := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &original); restoreErr != nil {
			t.Fatal(restoreErr)
		}
		if err == nil || !strings.Contains(err.Error(), "keyboard shm") {
			t.Fatalf("exhausted descriptor quota accepted keymap: %v", err)
		}
		if k.lastKeymap != "" || transport.writes != 0 {
			t.Fatalf("failed allocation changed keyboard state: cache=%q writes=%d", k.lastKeymap, transport.writes)
		}
		return
	}
	args := []string{"-test.run=^TestVirtualKeyboardRejectsKeymapWhenDescriptorQuotaExhausted$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), child+"=1", "XDG_RUNTIME_DIR=")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated descriptor exhaustion failed: %v\n%s", err, output)
	}
}

type cancelAtErrorContext struct {
	context.Context
	cancel   context.CancelFunc
	calls    int
	cancelAt int
}

func (c *cancelAtErrorContext) Err() error {
	c.calls++
	if c.calls == c.cancelAt {
		c.cancel()
	}
	return c.Context.Err()
}

func TestWaylandSendkeysCancellationStopsMultiRuneKeyAfterCurrentRune(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keyboard, _ := newSendkeysTestKeyboard()
	wire := &cancelAfterKeyboardMessage{
		cancel:      cancel,
		matchOpcode: 1,
		matchState:  0,
		matchKey:    true,
	}
	keyboard.ctx = wire

	err := keyboard.sendkeys(ctx, []keySend{{key: "ab"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("multi-rune key cancellation returned %v, want context.Canceled", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	if len(events) != 2 || events[0].state != 1 || events[1].state != 0 || events[0].keycode != events[1].keycode {
		t.Fatalf("canceled multi-rune key emitted events %+v, want one balanced rune tap", events)
	}
	if len(wire.msgs) != 3 {
		t.Fatalf("cancellation emitted %d messages, want one keymap and one balanced rune tap", len(wire.msgs))
	}
	if keyboard.mods != 0 || len(keyboard.held) != 0 {
		t.Fatalf("cancellation left keyboard state changed: modifiers=%d held=%v", keyboard.mods, keyboard.held)
	}
}

func TestWaylandTapCancellationAtReleaseCheckStillSendsKeyUp(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAtErrorContext{Context: base, cancel: cancel, cancelAt: 4}
	keyboard, wire := newTestKeyboard()

	err := keyboard.tap(ctx, kcDynBase)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("tap cancellation returned %v, want context.Canceled", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	if len(events) != 2 || events[0].state != 1 || events[1].state != 0 || events[0].keycode != events[1].keycode {
		t.Fatalf("canceled tap emitted events %+v, want one balanced key press", events)
	}
	if ctx.calls != ctx.cancelAt {
		t.Fatalf("context cancellation occurred at Err check %d, want %d", ctx.calls, ctx.cancelAt)
	}
}

// A canceled virtual keyboard request must not inspect held state or reject
// malformed text before acknowledging cancellation, even via direct helpers.
func TestVirtualKeyboardDirectKeyOperationsRejectCanceledCallerBeforeValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name   string
		invoke func(*wlKeyboard) error
	}{
		{"press invalid multi-character", func(k *wlKeyboard) error { return k.pressKey(ctx, "not_a_single_key") }},
		{"release invalid multi-character", func(k *wlKeyboard) error { return k.releaseKey(ctx, "not_a_single_key") }},
		{"release unheld modifier", func(k *wlKeyboard) error { return k.releaseKey(ctx, "ctrl") }},
		{"release unheld literal", func(k *wlKeyboard) error { return k.releaseKey(ctx, "x") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			err := tc.invoke(k)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation returned %v", err)
			}
			if len(wire.msgs) != 0 || len(k.held) != 0 || k.mods != 0 {
				t.Fatalf("canceled key request changed state or emitted messages")
			}
		})
	}
}

func TestWlKeyboardSendkeysFailedModifierReleaseCompensatesAtWire(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failAt     int
		wantKeyUps int
	}{
		{name: "key-up write", failAt: 2, wantKeyUps: 1},
		{name: "modifier state write", failAt: 3, wantKeyUps: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyboard, _ := newTestKeyboard()
			keyboard.mods = modControl
			wire := &failOnceKeyboardWrite{failAt: tc.failAt}
			keyboard.ctx = wire

			err := keyboard.sendkeys(context.Background(), []keySend{{key: "ctrl", up: true}})
			if err == nil || !strings.Contains(err.Error(), "injected one-time keyboard write failure") {
				t.Fatalf("failed modifier release returned %v", err)
			}
			if keyboard.mods != 0 {
				t.Fatalf("successful compensation retained modifier mask %#x", keyboard.mods)
			}
			events := keyboardEventsFromMessages(t, wire.msgs)
			keyUps := 0
			modifierStates := make([]uint32, 0, 2)
			for _, event := range events {
				switch event.opcode {
				case 1:
					if event.keycode == kcCtrl && event.state == 0 {
						keyUps++
					}
				case 2:
					modifierStates = append(modifierStates, event.state)
				}
			}
			if keyUps != tc.wantKeyUps {
				t.Fatalf("modifier key-up wire events=%d, want %d: %+v", keyUps, tc.wantKeyUps, events)
			}
			if len(modifierStates) < 2 || modifierStates[0] != modControl || modifierStates[len(modifierStates)-1] != 0 {
				t.Fatalf("modifier state was not restored after failed release: %+v", events)
			}
		})
	}
}

func TestWlKeyboardSendkeysFailedHeldReleaseCanBeRetried(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		slot uint32
	}{
		{name: "named key", key: "enter", slot: kcReturn},
		{name: "literal key", key: "é", slot: kcDynBase},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyboard, _ := newTestKeyboard()
			keyboard.held[tc.key] = tc.slot
			wire := &failingCtx{successWrites: 1}
			keyboard.ctx = wire

			err := keyboard.sendkeys(context.Background(), []keySend{{key: tc.key, up: true}})
			if err == nil || !strings.Contains(err.Error(), "injected write failure") {
				t.Fatalf("failed held-key release returned %v", err)
			}
			if slot, held := keyboard.held[tc.key]; !held || slot != tc.slot {
				t.Fatalf("failed release lost held-key ownership: slot=%d held=%v", slot, held)
			}

			recovery := &recordingCtx{}
			keyboard.ctx = recovery
			if err := keyboard.sendkeys(context.Background(), []keySend{{key: tc.key, up: true}}); err != nil {
				t.Fatalf("retry held-key release: %v", err)
			}
			events := keyboardEventsFromMessages(t, recovery.msgs)
			if len(events) != 1 || events[0].opcode != 1 || events[0].keycode != tc.slot || events[0].state != 0 {
				t.Fatalf("retry did not release original key slot %d: %+v", tc.slot, events)
			}
			if _, held := keyboard.held[tc.key]; held {
				t.Fatalf("successful retry retained held key: %+v", keyboard.held)
			}
		})
	}
}

func TestVirtualKeyboardDirectLiteralReleaseRetainsHeldSlotAfterWireFailure(t *testing.T) {
	keyboard, _ := newTestKeyboard()
	const key = "é"
	keyboard.held[key] = kcDynBase
	keyboard.lastKeymap = xkbWithRunes([]rune(key))
	peer := &failingCtx{successWrites: 0}
	keyboard.ctx = peer
	err := keyboard.releaseKey(context.Background(), key)
	if err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("failed key-up returned %v", err)
	}
	if slot, ok := keyboard.held[key]; !ok || slot != kcDynBase {
		t.Fatalf("failed key-up dropped tracked key: %#v", keyboard.held)
	}
	if peer.writes != 0 {
		t.Fatalf("failed key-up sent %d successful messages", peer.writes)
	}
	// A recovered compositor channel must still be able to release exactly the
	// originally held slot; losing bookkeeping makes recovery impossible.
	recovered := &recordingCtx{}
	keyboard.ctx = recovered
	if err := keyboard.releaseKey(context.Background(), key); err != nil {
		t.Fatalf("retry release after transport recovery: %v", err)
	}
	events := keyboardEventsFromMessages(t, recovered.msgs)
	if len(events) != 1 || events[0].opcode != 1 || events[0].keycode != kcDynBase || events[0].state != 0 {
		t.Fatalf("recovery did not release the original key: %+v", events)
	}
	if len(keyboard.held) != 0 {
		t.Fatalf("successful retry left held keys: %+v", keyboard.held)
	}
}

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

type keyboardWireEvent struct {
	opcode  uint32
	keycode uint32
	state   uint32
}

func keyboardEventsFromMessages(t *testing.T, msgs [][]byte) []keyboardWireEvent {
	t.Helper()
	var out []keyboardWireEvent
	for _, msg := range msgs {
		if len(msg) < 8 {
			t.Fatalf("truncated keyboard message %x", msg)
		}
		opcode := wl.Uint32(msg[4:8]) & 0xffff
		switch opcode {
		case 1:
			if len(msg) != 20 {
				t.Fatalf("malformed key event %x", msg)
			}
			out = append(out, keyboardWireEvent{opcode: 1, keycode: wl.Uint32(msg[12:16]) + 8, state: wl.Uint32(msg[16:20])})
		case 2:
			if len(msg) != 24 {
				t.Fatalf("malformed modifier event %x", msg)
			}
			out = append(out, keyboardWireEvent{opcode: 2, state: wl.Uint32(msg[8:12])})
		case 0:
			if len(msg) < 16 {
				t.Fatalf("malformed keymap event %x", msg)
			}
		default:
			t.Fatalf("unexpected keyboard wire opcode %d", opcode)
		}
	}
	return out
}
func TestVirtualKeyboardMixedTextNamedAndAcceleratorsPreserveProtocolOrder(t *testing.T) {
	k, ctx := newTestKeyboard()
	actions := []keySend{{text: "a"}, {key: "enter"}, {key: "b", modifiers: modifiers{ctrl: true}}, {text: "É"}}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, ctx.msgs)
	if len(events) != 10 {
		t.Fatalf("wrong number of key/modifier operations: %+v", events)
	}
	for _, pair := range [][2]int{{0, 1}, {2, 3}, {5, 6}, {8, 9}} {
		if events[pair[0]].opcode != 1 || events[pair[0]].state != 1 || events[pair[1]].opcode != 1 || events[pair[1]].state != 0 || events[pair[0]].keycode != events[pair[1]].keycode {
			t.Fatalf("unbalanced key pair at %d: %+v", pair[0], events)
		}
	}
	if events[4].opcode != 2 || events[4].state != modControl || events[7].opcode != 2 || events[7].state != 0 {
		t.Fatalf("Ctrl accelerator modifier missing: %+v", events)
	}
	if k.mods != 0 || len(k.held) != 0 {
		t.Fatalf("mixed typing left pressed keys: mods=%d held=%v", k.mods, k.held)
	}
}
func TestVirtualKeyboardExplicitHeldModifierSurvivesInterleavedTextUntilRelease(t *testing.T) {
	k, ctx := newTestKeyboard()
	actions := []keySend{{key: "ctrl", down: true}, {key: "a"}, {text: "b"}, {key: "ctrl", up: true}}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, ctx.msgs)
	var mods []uint32
	for _, ev := range events {
		if ev.opcode == 2 {
			mods = append(mods, ev.state)
		}
	}
	if len(mods) != 2 || mods[0] != modControl || mods[1] != 0 {
		t.Fatalf("held Ctrl unexpectedly released during input: events=%+v", events)
	}
	if k.mods != 0 || len(k.held) != 0 {
		t.Fatalf("modifier remained held: mods=%d held=%v", k.mods, k.held)
	}
}
func TestVirtualKeyboardExplicitHeldNamedAndLiteralKeysAreBalanced(t *testing.T) {
	for _, key := range []string{"enter", "é"} {
		t.Run(key, func(t *testing.T) {
			k, ctx := newTestKeyboard()
			if err := k.sendkeys(context.Background(), []keySend{{key: key, down: true}, {key: key, up: true}}); err != nil {
				t.Fatal(err)
			}
			events := keyboardEventsFromMessages(t, ctx.msgs)
			if len(events) != 2 || events[0].opcode != 1 || events[0].state != 1 || events[1].opcode != 1 || events[1].state != 0 || events[0].keycode != events[1].keycode {
				t.Fatalf("bad held-key protocol sequence for %q: %+v", key, events)
			}
			if len(k.held) != 0 {
				t.Fatalf("held %q survived its release: %v", key, k.held)
			}
		})
	}
}
func TestVirtualKeyboardBadHoldOrReleaseNeverInjectsKeyTransitions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action keySend
		want   string
	}{
		{"unknown named key up", keySend{key: "f3", up: true}, "not held"},
		{"literal not held", keySend{key: "x", up: true}, "not held"},
		{"multi rune hold", keySend{key: "abc", down: true}, "cannot hold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx := newTestKeyboard()
			err := k.sendkeys(context.Background(), []keySend{tc.action})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unsafe key action accepted: %v", err)
			}
			for _, ev := range keyboardEventsFromMessages(t, ctx.msgs) {
				if ev.opcode == 1 {
					t.Fatalf("invalid action injected key before rejection: %+v", ev)
				}
			}
			if len(k.held) != 0 || k.mods != 0 {
				t.Fatalf("invalid action retained state: %v %d", k.held, k.mods)
			}
		})
	}
}

type cancelOnKeyboardPress struct {
	recordingCtx
	cancel  context.CancelFunc
	pressed bool
}

func (c *cancelOnKeyboardPress) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := c.WriteMsg(data, oob)
	if err == nil && !c.pressed && len(data) == 20 && wl.Uint32(data[4:8])&0xffff == 1 && wl.Uint32(data[16:20]) == 1 {
		c.pressed = true
		c.cancel()
	}
	return err
}
func TestVirtualKeyboardCanceledAfterPressStillReleasesKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k, _ := newTestKeyboard()
	wire := &cancelOnKeyboardPress{cancel: cancel}
	k.ctx = wire
	err := k.sendkeys(ctx, []keySend{{key: "enter"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation during held key returned %v", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	if len(events) != 2 || events[0].state != 1 || events[1].state != 0 || events[0].keycode != events[1].keycode {
		t.Fatalf("canceled key was left pressed: %+v", events)
	}
}

type cancelAfterKeyboardRelease struct {
	recordingCtx
	cancel context.CancelFunc
}

func (c *cancelAfterKeyboardRelease) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.WriteMsg(data, oob); err != nil {
		return err
	}
	if len(data) == 20 && wl.Uint32(data[4:8])&0xffff == 1 && wl.Uint32(data[16:20]) == 0 {
		c.cancel()
	}
	return nil
}

func TestVirtualKeyboardCancellationAfterKeyReleaseStillReleasesTemporaryModifiers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keyboard, _ := newTestKeyboard()
	wire := &cancelAfterKeyboardRelease{cancel: cancel}
	keyboard.ctx = wire

	err := keyboard.sendkeys(ctx, []keySend{{key: "enter", modifiers: modifiers{ctrl: true}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled key sequence returned %v, want context.Canceled", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	var modifierStates []uint32
	for _, event := range events {
		if event.opcode == 2 {
			modifierStates = append(modifierStates, event.state)
		}
	}
	if len(modifierStates) != 2 || modifierStates[0] != modControl || modifierStates[1] != 0 {
		t.Fatalf("temporary modifier remained pressed after cancellation: %+v", events)
	}
	if keyboard.mods != 0 || len(keyboard.held) != 0 {
		t.Fatalf("canceled sequence retained key state: mods=%d held=%v", keyboard.mods, keyboard.held)
	}
}

type failOnceKeyboardWrite struct {
	recordingCtx
	failAt int
	failed bool
}

func (c *failOnceKeyboardWrite) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.writes == c.failAt && !c.failed {
		c.failed = true
		return errors.New("injected one-time keyboard write failure")
	}
	return c.recordingCtx.WriteMsg(data, oob)
}

func TestVirtualKeyboardModifierPressFailureReleasesModifierKey(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		action keySend
		failAt int
	}{
		{name: "hold state publication", action: keySend{key: "ctrl", down: true}, failAt: 2},
		{name: "tap state publication", action: keySend{key: "ctrl"}, failAt: 2},
		{name: "tap key release", action: keySend{key: "ctrl"}, failAt: 3},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			keyboard, _ := newTestKeyboard()
			wire := &failOnceKeyboardWrite{failAt: testCase.failAt}
			keyboard.ctx = wire

			err := keyboard.sendkeys(context.Background(), []keySend{testCase.action})
			if err == nil || !strings.Contains(err.Error(), "injected one-time keyboard write failure") {
				t.Fatalf("modifier sequence error = %v, want injected modifier-state failure", err)
			}
			events := keyboardEventsFromMessages(t, wire.msgs)
			var keys []keyboardWireEvent
			var modifierStates []uint32
			for _, event := range events {
				if event.opcode == 1 {
					keys = append(keys, event)
				} else if event.opcode == 2 {
					modifierStates = append(modifierStates, event.state)
				}
			}
			if len(keys) != 2 || keys[0].keycode != kcCtrl || keys[0].state != 1 || keys[1].keycode != kcCtrl || keys[1].state != 0 {
				t.Fatalf("failed modifier operation left the key pressed: %+v", events)
			}
			if len(modifierStates) == 0 || modifierStates[len(modifierStates)-1] != 0 {
				t.Fatalf("modifier state not cleared after failed operation: %+v", events)
			}
			if keyboard.mods != 0 || len(keyboard.held) != 0 {
				t.Fatalf("failed modifier press retained logical state: mods=%d held=%v", keyboard.mods, keyboard.held)
			}
		})
	}
}

func TestVirtualKeyboardCombinedTemporaryModifierMaskIsBalanced(t *testing.T) {
	keyboard, wire := newTestKeyboard()
	allModifiers := modifiers{ctrl: true, alt: true, shift: true, super: true}
	if err := keyboard.sendkeys(context.Background(), []keySend{{key: "enter", modifiers: allModifiers}}); err != nil {
		t.Fatalf("send key with all temporary modifiers: %v", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	var modifierStates []uint32
	var keys []keyboardWireEvent
	for _, event := range events {
		if event.opcode == 2 {
			modifierStates = append(modifierStates, event.state)
		} else if event.opcode == 1 {
			keys = append(keys, event)
		}
	}
	wantMask := modControl | modMod1 | modShift | modMod4
	if len(modifierStates) != 2 || modifierStates[0] != wantMask || modifierStates[1] != 0 {
		t.Fatalf("combined temporary modifier states = %v, want [%d 0]", modifierStates, wantMask)
	}
	if len(keys) != 2 || keys[0].state != 1 || keys[1].state != 0 || keys[0].keycode != keys[1].keycode {
		t.Fatalf("modified key events are not balanced: %+v", keys)
	}
}

func TestVirtualKeyboardFailedTemporaryModifierSetupNeverDispatchesKey(t *testing.T) {
	keyboard, _ := newTestKeyboard()
	wire := &failOnceKeyboardWrite{failAt: 1}
	keyboard.ctx = wire
	err := keyboard.sendkeys(context.Background(), []keySend{{key: "enter", modifiers: modifiers{shift: true}}})
	if err == nil || !strings.Contains(err.Error(), "injected one-time keyboard write failure") {
		t.Fatalf("failed temporary modifier setup returned %v, want the injected failure", err)
	}
	for _, event := range keyboardEventsFromMessages(t, wire.msgs) {
		if event.opcode == 1 {
			t.Fatalf("key dispatched after temporary modifier setup failed: %+v", event)
		}
	}
	if keyboard.mods != 0 || len(keyboard.held) != 0 {
		t.Fatalf("failed temporary modifier setup retained key state: mods=%d held=%v", keyboard.mods, keyboard.held)
	}
}

func TestVirtualKeyboardRetainsModifierStateWhenCleanupTransportFails(t *testing.T) {
	keyboard, wire := newFailingKeyboard(3)
	err := keyboard.sendkeys(context.Background(), []keySend{{key: "ctrl"}})
	if err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("modifier cleanup failure returned %v, want the injected transport error", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	if len(events) != 2 || events[0].opcode != 1 || events[0].keycode != kcCtrl || events[0].state != 1 || events[1].opcode != 2 || events[1].state != modControl {
		t.Fatalf("unexpected wire state after cleanup transport failure: %+v", events)
	}
	if keyboard.mods != modControl {
		t.Fatalf("last known modifier state = %d, want %d", keyboard.mods, modControl)
	}
}

type failSelectedKeyboardWrites struct {
	recordingCtx
	failAt map[int]struct{}
}

func (c *failSelectedKeyboardWrites) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, fail := c.failAt[c.writes]; fail {
		delete(c.failAt, c.writes)
		return errors.New("injected selected keyboard write failure")
	}
	return c.recordingCtx.WriteMsg(data, oob)
}

func TestVirtualKeyboardRetainsModifierStateWhenModifierCleanupPublicationFails(t *testing.T) {
	keyboard, _ := newTestKeyboard()
	wire := &failSelectedKeyboardWrites{failAt: map[int]struct{}{4: {}, 5: {}}}
	keyboard.ctx = wire

	err := keyboard.sendkeys(context.Background(), []keySend{{key: "ctrl"}})
	if err == nil || !strings.Contains(err.Error(), "injected selected keyboard write failure") {
		t.Fatalf("modifier cleanup error = %v, want the injected state publication failure", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	if len(events) != 4 || events[0].opcode != 1 || events[0].keycode != kcCtrl || events[0].state != 1 || events[1].opcode != 2 || events[1].state != modControl || events[2].opcode != 1 || events[2].keycode != kcCtrl || events[2].state != 0 || events[3].opcode != 1 || events[3].keycode != kcCtrl || events[3].state != 0 {
		t.Fatalf("unexpected wire state after modifier cleanup publication failure: %+v", events)
	}
	if keyboard.mods != modControl {
		t.Fatalf("last published modifier state = %d, want %d", keyboard.mods, modControl)
	}
}

// A malformed later action must not inject an earlier valid keyboard prefix.
// The complete command is one user operation, not an instruction to type until
// encountering the first provably invalid hold or release.
func TestVirtualKeyboardInvalidSequenceCannotApplyEarlierValidPrefix(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial []keySend
		actions []keySend
		message string
	}{
		{"release unheld literal after valid tap", nil, []keySend{{key: "a"}, {key: "b", up: true}}, "not held"},
		{"release unheld named after valid tap", nil, []keySend{{text: "text"}, {key: "enter", up: true}}, "not held"},
		{"release unheld modifier after valid tap", nil, []keySend{{key: "a"}, {key: "ctrl", up: true}}, "not held"},
		{"hold multi-rune after text", nil, []keySend{{text: "hello"}, {key: "bad", down: true}}, "cannot hold"},
		{"double release after valid hold", nil, []keySend{{key: "enter", down: true}, {key: "enter", up: true}, {key: "enter", up: true}}, "not held"},
		{"modifier release double", nil, []keySend{{key: "ctrl", down: true}, {key: "ctrl", up: true}, {key: "ctrl", up: true}}, "not held"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			if len(tc.initial) > 0 {
				if err := k.sendkeys(context.Background(), tc.initial); err != nil {
					t.Fatal(err)
				}
			}
			before := len(wire.msgs)
			err := k.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("invalid keyboard sequence accepted: %v", err)
			}
			if len(wire.msgs) != before {
				t.Fatalf("invalid trailing action injected %d premature compositor wire messages: %v", len(wire.msgs)-before, keyboardEventsFromMessages(t, wire.msgs[before:]))
			}
			if k.mods != 0 || len(k.held) != 0 {
				t.Fatalf("invalid sequence changed held state mods=%d held=%v", k.mods, k.held)
			}
		})
	}
}
func TestVirtualKeyboardSequencePreflightHonorsPriorHeldState(t *testing.T) {
	k, wire := newTestKeyboard()
	if err := k.sendkeys(context.Background(), []keySend{{key: "enter", down: true}, {key: "ctrl", down: true}}); err != nil {
		t.Fatal(err)
	}
	before := len(wire.msgs)
	if err := k.sendkeys(context.Background(), []keySend{{key: "enter", up: true}, {key: "ctrl", up: true}}); err != nil {
		t.Fatalf("valid prior-key release rejected: %v", err)
	}
	if len(wire.msgs) <= before || k.mods != 0 || len(k.held) != 0 {
		t.Fatalf("valid held-key release not sent: mods=%d held=%v messages=%d", k.mods, k.held, len(wire.msgs)-before)
	}
}

type uncertainDirectKeyPressWire struct {
	recordingCtx
	pressErr    error
	cleanupErr  error
	cancel      context.CancelFunc
	failCleanup bool
}

func (w *uncertainDirectKeyPressWire) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.WriteMsg(data, oob); err != nil {
		return err
	}
	if len(data) == 20 && wl.Uint32(data[4:8])&0xffff == 1 {
		if wl.Uint32(data[16:20]) == 1 {
			if w.cancel != nil {
				w.cancel()
			}
			return w.pressErr
		}
		if w.failCleanup {
			return w.cleanupErr
		}
	}
	return nil
}

// A failed key-down may have reached the compositor; a failed explicit hold
// must release the uncertain key without tracking ownership of the failed hold.
func TestWaylandDirectFailedKeyDownCompensatesAtWire(t *testing.T) { //nolint:gocyclo // Three key kinds and three transport outcomes protect independent hold paths.
	for _, key := range []struct {
		name  string
		value string
		code  uint32
	}{
		{name: "modifier", value: "ctrl", code: kcCtrl},
		{name: "named key", value: "enter", code: kcReturn},
		{name: "literal", value: "é", code: kcDynBase},
	} {
		for _, tc := range []struct {
			name                 string
			cancel, cleanupFails bool
		}{
			{name: "release succeeds"},
			{name: "release fails", cleanupFails: true},
			{name: "cancel after uncertain press", cancel: true},
		} {
			t.Run(key.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pressErr := errors.New("press was delivered but reporting failed")
				cleanupErr := errors.New("key release cleanup transport failed")
				wire := &uncertainDirectKeyPressWire{pressErr: pressErr, cleanupErr: cleanupErr, failCleanup: tc.cleanupFails}
				if tc.cancel {
					wire.cancel = cancel
				}
				k, _ := newTestKeyboard()
				k.ctx = wire
				err := k.pressKey(ctx, key.value)
				if !errors.Is(err, pressErr) || errors.Is(err, cleanupErr) != tc.cleanupFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("lost press or cleanup errors: %v", err)
				}
				if k.mods != 0 || len(k.held) != 0 {
					t.Fatalf("failed hold retained local state: mods=%d held=%v", k.mods, k.held)
				}
				events := keyboardEventsFromMessages(t, wire.msgs)
				if len(events) != 2 || events[0].opcode != 1 || events[0].keycode != key.code || events[0].state != 1 || events[1].opcode != 1 || events[1].keycode != key.code || events[1].state != 0 {
					t.Fatalf("uncertain hold did not release key: %+v", events)
				}
			})
		}
	}
}

// Sequence-level explicit holds must release a possible compositor key-down
// that reported a transport error, without retaining ownership of the hold.
func TestWaylandSequenceFailedExplicitHoldCompensatesAtWire(t *testing.T) { //nolint:gocyclo // Three distinct key paths and three failure modes.
	for _, key := range []struct {
		name, value string
		code        uint32
	}{
		{name: "modifier", value: "ctrl", code: kcCtrl},
		{name: "named key", value: "enter", code: 99},
		{name: "literal", value: "é", code: 99},
	} {
		for _, tc := range []struct {
			name                 string
			cancel, cleanupFails bool
		}{
			{name: "release succeeds"},
			{name: "release fails", cleanupFails: true},
			{name: "cancel after uncertain press", cancel: true},
		} {
			t.Run(key.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pressErr := errors.New("press may have reached compositor")
				cleanupErr := errors.New("compensating release failed")
				wire := &uncertainDirectKeyPressWire{pressErr: pressErr, cleanupErr: cleanupErr, failCleanup: tc.cleanupFails}
				if tc.cancel {
					wire.cancel = cancel
				}
				k, _ := newTestKeyboard()
				k.ctx = wire
				err := k.sendkeys(ctx, []keySend{{key: key.value, down: true}})
				if !errors.Is(err, pressErr) || errors.Is(err, cleanupErr) != tc.cleanupFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("sequence lost input or cleanup error: %v", err)
				}
				if k.mods != 0 || len(k.held) != 0 {
					t.Fatalf("failed hold retained local state: mods=%d held=%v", k.mods, k.held)
				}
				events := keyboardEventsFromMessages(t, wire.msgs)
				if len(events) != 2 || events[0].opcode != 1 || events[0].keycode != key.code || events[0].state != 1 || events[1].opcode != 1 || events[1].keycode != key.code || events[1].state != 0 {
					t.Fatalf("uncertain sequence hold not balanced: %+v", events)
				}
			})
		}
	}
}

// Tap requests that fail after delivery must still balance their key-down
// with an independent release; a failed cleanup must remain observable.
func TestWaylandFailedTapPressCompensatesAtWire(t *testing.T) { //nolint:gocyclo // Three distinct tap dispatchers and three transport outcomes.
	for _, method := range []struct {
		name string
		code uint32
		call func(*wlKeyboard, context.Context) error
	}{
		{name: "direct tap", code: kcDynBase, call: func(k *wlKeyboard, ctx context.Context) error { return k.tap(ctx, kcDynBase) }},
		{name: "named sequence tap", code: 99, call: func(k *wlKeyboard, ctx context.Context) error { return k.sendkeys(ctx, []keySend{{key: "enter"}}) }},
		{name: "modifier sequence tap", code: kcCtrl, call: func(k *wlKeyboard, ctx context.Context) error { return k.sendkeys(ctx, []keySend{{key: "ctrl"}}) }},
	} {
		for _, tc := range []struct {
			name                 string
			cancel, cleanupFails bool
		}{
			{name: "release succeeds"},
			{name: "release fails", cleanupFails: true},
			{name: "cancel after uncertain press", cancel: true},
		} {
			t.Run(method.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pressErr := errors.New("XKB tap press reached compositor before transport error")
				cleanupErr := errors.New("XKB tap cleanup failed")
				wire := &uncertainDirectKeyPressWire{pressErr: pressErr, cleanupErr: cleanupErr, failCleanup: tc.cleanupFails}
				if tc.cancel {
					wire.cancel = cancel
				}
				k, _ := newTestKeyboard()
				k.ctx = wire
				err := method.call(k, ctx)
				if !errors.Is(err, pressErr) || errors.Is(err, cleanupErr) != tc.cleanupFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("tap lost primary error, cancellation or release error: %v", err)
				}
				if k.mods != 0 || len(k.held) != 0 {
					t.Fatalf("failed tap retained state: mods=%d held=%v", k.mods, k.held)
				}
				events := keyboardEventsFromMessages(t, wire.msgs)
				if len(events) != 2 || events[0].opcode != 1 || events[0].keycode != method.code || events[0].state != 1 || events[1].opcode != 1 || events[1].keycode != method.code || events[1].state != 0 {
					t.Fatalf("failed tap did not balance possible press: %+v", events)
				}
			})
		}
	}
}

func TestVirtualKeyboardDirectReleaseRejectsUnheldModifiersWithoutWireEvent(t *testing.T) {
	for _, key := range []string{"ctrl", "shift", "alt", "super"} {
		t.Run(key, func(t *testing.T) {
			keyboard, wire := newTestKeyboard()
			if err := keyboard.releaseKey(context.Background(), key); err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("release of unheld modifier %q returned %v", key, err)
			}
			if len(wire.msgs) != 0 {
				t.Fatalf("release of unheld modifier emitted %d compositor messages", len(wire.msgs))
			}
			if keyboard.mods != 0 {
				t.Fatalf("unheld modifier changed logical state to %#x", keyboard.mods)
			}
		})
	}
}

type warmupShiftFailureWire struct {
	recordingCtx
	cancel                               context.CancelFunc
	pressErr, errorRelease, errorCleanup error
	releaseAttempts                      int
}

func (w *warmupShiftFailureWire) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.WriteMsg(data, oob); err != nil {
		return err
	}
	if len(data) == 20 && wl.Uint32(data[4:8])&0xffff == 1 {
		state := wl.Uint32(data[16:20])
		if state == 1 {
			if w.cancel != nil {
				w.cancel()
			}
			return w.pressErr
		}
		w.releaseAttempts++
		if w.releaseAttempts == 1 {
			return w.errorRelease
		}
		return w.errorCleanup
	}
	return nil
}

func TestWaylandKeyboardWarmupCompensatesForUncertainShiftState(t *testing.T) { //nolint:gocyclo // Canceled and failed press/release cases use independent wire transitions.
	for _, tc := range []struct {
		name                                                string
		cancelAfterDown, failDown, failRelease, failCleanup bool
	}{
		{name: "canceled after successful down", cancelAfterDown: true},
		{name: "failed down may have reached compositor", failDown: true},
		{name: "canceled failed down", cancelAfterDown: true, failDown: true},
		{name: "release fails once", failRelease: true},
		{name: "both releases fail", failRelease: true, failCleanup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			downErr := errors.New("warmup shift press was uncertain")
			releaseErr := errors.New("warmup shift release failed")
			cleanupErr := errors.New("warmup shift cleanup failed")
			wire := &warmupShiftFailureWire{}
			if tc.cancelAfterDown {
				wire.cancel = cancel
			}
			if tc.failDown {
				wire.pressErr = downErr
			}
			if tc.failRelease {
				wire.errorRelease = releaseErr
			}
			if tc.failCleanup {
				wire.errorCleanup = cleanupErr
			}
			k, _ := newTestKeyboard()
			k.ctx = wire
			err := k.warmupContext(ctx)
			if tc.cancelAfterDown && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if tc.failDown && !errors.Is(err, downErr) {
				t.Fatalf("lost shift-down error: %v", err)
			}
			if tc.failRelease && !errors.Is(err, releaseErr) {
				t.Fatalf("lost shift-up error: %v", err)
			}
			if tc.failCleanup && !errors.Is(err, cleanupErr) {
				t.Fatalf("lost cleanup error: %v", err)
			}
			wantReleases := 1
			if tc.failRelease {
				wantReleases = 2
			}
			if wire.releaseAttempts != wantReleases {
				t.Fatalf("warmup released Shift %d times, want %d", wire.releaseAttempts, wantReleases)
			}
			events := keyboardEventsFromMessages(t, wire.msgs)
			if len(events) != 1+wantReleases {
				t.Fatalf("warmup did not send shift-down and cleanup: %+v", events)
			}
			for _, ev := range events[1:] {
				if ev.opcode != 1 || ev.keycode != kcShift || ev.state != 0 {
					t.Fatalf("warmup cleanup did not release Shift: %+v", events)
				}
			}
		})
	}
}

// recordingCtx extends fakeCtx to capture all messages for inspection.
type recordingCtx struct {
	writes   int
	msgs     [][]byte
	lastData []byte
}

func (r *recordingCtx) Register(p wl.Proxy)            {}
func (r *recordingCtx) SetProxy(id uint32, p wl.Proxy) {}
func (r *recordingCtx) Unregister(p wl.Proxy)          {}
func (r *recordingCtx) WriteMsg(data, oob []byte) error {
	r.writes++
	r.lastData = append([]byte{}, data...)
	r.msgs = append(r.msgs, append([]byte{}, data...))
	return nil
}
func (r *recordingCtx) Dispatch() error { return nil }
func (r *recordingCtx) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.WriteMsg(data, oob)
}
func (r *recordingCtx) DispatchContext(ctx context.Context) error { return ctx.Err() }
func (r *recordingCtx) WithOperationContext(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}
func (r *recordingCtx) Close() error { return nil }

func newTestKeyboard() (*wlKeyboard, *recordingCtx) {
	k := &wlKeyboard{held: make(map[string]uint32)}
	rp := &wl.RawProxy{}
	rp.SetID(42)
	k.kbd = rp
	f := &recordingCtx{}
	k.ctx = f
	return k, f
}

// failingCtx is a recordingCtx that returns an error after successWrites writes.
type failingCtx struct {
	recordingCtx
	successWrites int // number of writes to allow before failing
}

func (f *failingCtx) WriteMsg(data, oob []byte) error {
	if f.writes >= f.successWrites {
		return errors.New("injected write failure")
	}
	return f.recordingCtx.WriteMsg(data, oob)
}

func (f *failingCtx) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.WriteMsg(data, oob)
}

func newFailingKeyboard(successWrites int) (*wlKeyboard, *failingCtx) { //nolint:unparam
	k := &wlKeyboard{held: make(map[string]uint32)}
	rp := &wl.RawProxy{}
	rp.SetID(42)
	k.kbd = rp
	f := &failingCtx{successWrites: successWrites}
	k.ctx = f
	return k, f
}

func TestSendKey_MessageFormat(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.sendKey(context.Background(), 10, 1); err != nil {
		t.Fatalf("sendKey: %v", err)
	}
	if rc.writes != 1 {
		t.Fatalf("expected 1 write, got %d", rc.writes)
	}
	msg := rc.lastData
	// Message: id(4) + size|opcode(4) + time(4) + keycode-8(4) + state(4) = 20 bytes
	if len(msg) != 20 {
		t.Fatalf("message length = %d, want 20", len(msg))
	}
	// Check object ID
	if wl.Uint32(msg[0:4]) != 42 {
		t.Errorf("object ID = %d, want 42", wl.Uint32(msg[0:4]))
	}
	// Check size|opcode: size=20, opcode=1
	sizeOpcode := wl.Uint32(msg[4:8])
	if sizeOpcode>>16 != 20 {
		t.Errorf("size = %d, want 20", sizeOpcode>>16)
	}
	if sizeOpcode&0xffff != 1 {
		t.Errorf("opcode = %d, want 1 (key)", sizeOpcode&0xffff)
	}
	// Check keycode: 10-8 = 2
	if wl.Uint32(msg[12:16]) != 2 {
		t.Errorf("keycode = %d, want 2", wl.Uint32(msg[12:16]))
	}
	// Check state: 1 (pressed)
	if wl.Uint32(msg[16:20]) != 1 {
		t.Errorf("state = %d, want 1", wl.Uint32(msg[16:20]))
	}
}

func TestSendKey_Release(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.sendKey(context.Background(), 10, 0); err != nil {
		t.Fatalf("sendKey: %v", err)
	}
	msg := rc.lastData
	// state should be 0
	if wl.Uint32(msg[16:20]) != 0 {
		t.Errorf("state = %d, want 0 (release)", wl.Uint32(msg[16:20]))
	}
}

func TestSendKeyRejectsInvalidKeycodeAndState(t *testing.T) {
	k, rc := newTestKeyboard()

	if err := k.sendKey(context.Background(), 7, 1); err == nil {
		t.Fatal("sendKey accepted a keycode below the XKB base")
	}
	if err := k.sendKey(context.Background(), 8, 2); err == nil {
		t.Fatal("sendKey accepted an invalid key state")
	}
	if rc.writes != 0 {
		t.Fatalf("invalid sendKey calls produced %d writes", rc.writes)
	}
}

func TestSendModifiers_MessageFormat(t *testing.T) {
	k, rc := newTestKeyboard()
	k.mods = modShift | modControl
	if err := k.sendModifiers(context.Background()); err != nil {
		t.Fatalf("sendModifiers: %v", err)
	}
	if rc.writes != 1 {
		t.Fatalf("expected 1 write, got %d", rc.writes)
	}
	msg := rc.lastData
	// Message: id(4) + size|opcode(4) + mods_depressed(4) + mods_latched(4) + mods_locked(4) + group(4) = 24 bytes
	if len(msg) != 24 {
		t.Fatalf("message length = %d, want 24", len(msg))
	}
	// Check size|opcode: size=24, opcode=2
	sizeOpcode := wl.Uint32(msg[4:8])
	if sizeOpcode>>16 != 24 {
		t.Errorf("size = %d, want 24", sizeOpcode>>16)
	}
	if sizeOpcode&0xffff != 2 {
		t.Errorf("opcode = %d, want 2 (modifiers)", sizeOpcode&0xffff)
	}
	// Check mods_depressed
	mods := wl.Uint32(msg[8:12])
	if mods != modShift|modControl {
		t.Errorf("mods = 0x%x, want 0x%x", mods, modShift|modControl)
	}
	// latched, locked, group should all be 0
	for i := 12; i < 24; i += 4 {
		if wl.Uint32(msg[i:i+4]) != 0 {
			t.Errorf("field at offset %d = %d, want 0", i, wl.Uint32(msg[i:i+4]))
		}
	}
}

func TestSendModifiers_ZeroMods(t *testing.T) {
	k, rc := newTestKeyboard()
	k.mods = 0
	if err := k.sendModifiers(context.Background()); err != nil {
		t.Fatalf("sendModifiers: %v", err)
	}
	msg := rc.lastData
	mods := wl.Uint32(msg[8:12])
	if mods != 0 {
		t.Errorf("mods = 0x%x, want 0", mods)
	}
}

func TestTap_PressAndRelease(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.tap(context.Background(), 15); err != nil {
		t.Fatalf("tap: %v", err)
	}
	// tap sends key down then key up = 2 messages
	if rc.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", rc.writes)
	}
	// First message: state=1 (press)
	if wl.Uint32(rc.msgs[0][16:20]) != 1 {
		t.Errorf("first event state = %d, want 1 (press)", wl.Uint32(rc.msgs[0][16:20]))
	}
	// Second message: state=0 (release)
	if wl.Uint32(rc.msgs[1][16:20]) != 0 {
		t.Errorf("second event state = %d, want 0 (release)", wl.Uint32(rc.msgs[1][16:20]))
	}
	// Both should have keycode = 15-8 = 7
	for i, msg := range rc.msgs {
		kc := wl.Uint32(msg[12:16])
		if kc != 7 {
			t.Errorf("msg[%d] keycode = %d, want 7", i, kc)
		}
	}
}

func TestUploadKeymapAndRestoreMods_WithMods(t *testing.T) {
	k, rc := newTestKeyboard()
	k.mods = modShift
	if err := k.uploadKeymapAndRestoreMods(context.Background(), xkbModsOnly()); err != nil {
		t.Fatalf("uploadKeymapAndRestoreMods: %v", err)
	}
	// Should have: 1 keymap upload + 1 modifiers message
	if rc.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", rc.writes)
	}
	// Second message should be modifiers
	msg := rc.msgs[1]
	sizeOpcode := wl.Uint32(msg[4:8])
	if sizeOpcode&0xffff != 2 {
		t.Errorf("second msg opcode = %d, want 2 (modifiers)", sizeOpcode&0xffff)
	}
}

func TestUploadKeymapAndRestoreMods_NoMods(t *testing.T) {
	k, rc := newTestKeyboard()
	k.mods = 0
	if err := k.uploadKeymapAndRestoreMods(context.Background(), xkbModsOnly()); err != nil {
		t.Fatalf("uploadKeymapAndRestoreMods: %v", err)
	}
	// Should have only 1 write (keymap upload, no modifiers needed)
	if rc.writes != 1 {
		t.Fatalf("expected 1 write, got %d", rc.writes)
	}
}

func TestPressKey_Modifier(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.pressKey(context.Background(), "shift"); err != nil {
		t.Fatalf("pressKey: %v", err)
	}
	// 1 keymap upload + 1 key press + 1 modifiers = 3 writes
	if rc.writes != 3 {
		t.Fatalf("expected 3 writes, got %d", rc.writes)
	}
	// Verify shift is now in mods
	if k.mods&modShift == 0 {
		t.Error("shift modifier not set")
	}
}

func TestPressKey_NamedNonModifier(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.pressKey(context.Background(), "return"); err != nil {
		t.Fatalf("pressKey: %v", err)
	}
	// 1 keymap upload + 1 key press = 2 writes (no modifiers message)
	if rc.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", rc.writes)
	}
	// Key should be in held map
	if _, ok := k.held["return"]; !ok {
		t.Error("return key not in held map")
	}
}

func TestPressKey_SingleChar(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.pressKey(context.Background(), "x"); err != nil {
		t.Fatalf("pressKey: %v", err)
	}
	// 1 keymap upload + 1 key press = 2 writes
	if rc.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", rc.writes)
	}
	if _, ok := k.held["x"]; !ok {
		t.Error("x key not in held map")
	}
}

func TestPressKey_MultiCharError(t *testing.T) {
	k, _ := newTestKeyboard()
	err := k.pressKey(context.Background(), "abc")
	if err == nil {
		t.Fatal("expected error for multi-character key")
	}
	t.Logf("got expected error: %v", err)
}

func TestReleaseKey_Modifier(t *testing.T) {
	k, rc := newTestKeyboard()
	// Press then release shift
	if err := k.pressKey(context.Background(), "shift"); err != nil {
		t.Fatalf("pressKey: %v", err)
	}
	rc.writes = 0 // reset
	rc.msgs = nil

	if err := k.releaseKey(context.Background(), "shift"); err != nil {
		t.Fatalf("releaseKey: %v", err)
	}
	// 1 key release + 1 modifiers = 2 writes
	if rc.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", rc.writes)
	}
	// Verify shift is cleared from mods
	if k.mods&modShift != 0 {
		t.Error("shift modifier still set after release")
	}
}

func TestReleaseKey_NamedNonModifier(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.pressKey(context.Background(), "return"); err != nil {
		t.Fatalf("pressKey: %v", err)
	}
	rc.writes = 0
	rc.msgs = nil

	if err := k.releaseKey(context.Background(), "return"); err != nil {
		t.Fatalf("releaseKey: %v", err)
	}
	// releaseKey calls uploadKeymap which may be cached (same keymap as pressKey),
	// so we expect 1 key release write (keymap upload is cached/skipped).
	if rc.writes != 1 {
		t.Fatalf("expected 1 write, got %d", rc.writes)
	}
	// Key should be removed from held map
	if _, ok := k.held["return"]; ok {
		t.Error("return key still in held map after release")
	}
}

func TestReleaseKey_NotHeld(t *testing.T) {
	k, rc := newTestKeyboard()
	// Release a key that was never pressed — should return an error
	err := k.releaseKey(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error for releasing non-held key")
	}
	t.Logf("got expected error: %v", err)
	if rc.writes != 0 {
		t.Fatalf("expected 0 writes for unreleased key, got %d", rc.writes)
	}
}

func TestReleaseKey_NamedNotHeld(t *testing.T) {
	k, rc := newTestKeyboard()
	// Release a named non-modifier key that was never pressed — should return an error
	err := k.releaseKey(context.Background(), "return")
	if err == nil {
		t.Fatal("expected error for releasing non-held named key")
	}
	t.Logf("got expected error: %v", err)
	if rc.writes != 0 {
		t.Fatalf("expected 0 writes for unreleased key, got %d", rc.writes)
	}
}

func TestReleaseKey_ModifierNotHeld(t *testing.T) {
	k, rc := newTestKeyboard()
	// Direct KeyUp must obey the same held-state contract as the preflighted
	// sendkeys path: unheld modifiers must not inject stray key-up events.
	if err := k.releaseKey(context.Background(), "shift"); err == nil {
		t.Fatal("unheld Shift release unexpectedly succeeded")
	}
	if rc.writes != 0 || k.mods != 0 {
		t.Fatalf("unheld release changed compositor or logical state: writes=%d mods=%d", rc.writes, k.mods)
	}
}

func TestPressKey_ModifierStateRollsBackWhenUpdateFails(t *testing.T) {
	k, _ := newFailingKeyboard(2) // keymap and key-down succeed; modifiers fails

	err := k.pressKey(context.Background(), "shift")
	if err == nil {
		t.Fatal("pressKey succeeded despite modifier update failure")
	}
	if k.mods != 0 {
		t.Fatalf("k.mods = 0x%x after failed modifier press, want 0", k.mods)
	}
}

func TestReleaseKey_ModifierStateRollsBackWhenUpdateFails(t *testing.T) {
	k, _ := newFailingKeyboard(1) // key-up succeeds; modifiers update fails
	k.mods = modShift

	err := k.releaseKey(context.Background(), "shift")
	if err == nil {
		t.Fatal("releaseKey succeeded despite modifier update failure")
	}
	if k.mods != modShift {
		t.Fatalf("k.mods = 0x%x after failed modifier release, want 0x%x", k.mods, modShift)
	}
}

func TestUploadKeymap_MessageFormat(t *testing.T) {
	k, rc := newTestKeyboard()
	text := "xkb_keymap { }"
	if err := k.uploadKeymap(context.Background(), text); err != nil {
		t.Fatalf("uploadKeymap: %v", err)
	}
	msg := rc.lastData
	// Message: id(4) + size|opcode(4) + format(4) + size(4) = 16 bytes
	if len(msg) != 16 {
		t.Fatalf("message length = %d, want 16", len(msg))
	}
	// Check opcode 0 (keymap)
	sizeOpcode := wl.Uint32(msg[4:8])
	if sizeOpcode&0xffff != 0 {
		t.Errorf("opcode = %d, want 0 (keymap)", sizeOpcode&0xffff)
	}
	// Check format = 1 (xkb_format_text_v1)
	if wl.Uint32(msg[8:12]) != xkbFormatTextV1 {
		t.Errorf("format = %d, want %d", wl.Uint32(msg[8:12]), xkbFormatTextV1)
	}
}

func TestXkbModsOnly_ContainsExpectedKeys(t *testing.T) {
	km := xkbModsOnly()
	expected := []string{"Shift_L", "Control_L", "Alt_L", "Super_L"}
	for _, key := range expected {
		if !bytes.Contains([]byte(km), []byte(key)) {
			t.Errorf("xkbModsOnly missing %q", key)
		}
	}
}

func TestXkbWithRunes_Structure(t *testing.T) {
	km := xkbWithRunes([]rune{'A', 'b', '€'})
	if !bytes.Contains([]byte(km), []byte("[ A ]")) {
		t.Error("missing A keysym")
	}
	if !bytes.Contains([]byte(km), []byte("[ b ]")) {
		t.Error("missing b keysym")
	}
	if !bytes.Contains([]byte(km), []byte("U20AC")) {
		t.Error("missing € keysym")
	}
}

func TestXkbWithNamed_Structure(t *testing.T) {
	km := xkbWithNamed(kcReturn, "Return")
	if !bytes.Contains([]byte(km), []byte("Return")) {
		t.Error("missing Return keysym")
	}
	if !bytes.Contains([]byte(km), []byte("KNAM")) {
		t.Error("missing KNAM keycode")
	}
}

func TestXkbWithNamed_ModifierReturnsModsOnly(t *testing.T) {
	km := xkbWithNamed(kcShift, "Shift_L")
	if bytes.Contains([]byte(km), []byte("KNAM")) {
		t.Error("modifier keycode should not add KNAM")
	}
}

// uniqueRunes is shared with wl_keyboard_test.go.
func uniqueRunes(s string) []rune {
	seen := make(map[rune]bool)
	var out []rune
	for _, r := range s {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// TestSendkeysContext_HeldNotSetOnSendKeyFailure verifies that k.held is NOT
// populated when the sendKey call for a key-down action fails. Before the fix,
// k.held was set before sendKey, leaving a phantom held key on failure.
func TestSendkeysContext_HeldNotSetOnSendKeyFailure(t *testing.T) {
	// sendkeys does 1 write for keymap upload; the next write is sendKey.
	// Allow 1 write (keymap) then fail.
	k, _ := newFailingKeyboard(1)

	actions, err := parseKeySend("{enter down}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Should fail because sendKey (write 1) fails.
	if err := k.sendkeys(context.Background(), actions); err == nil {
		t.Fatal("expected error from injected write failure")
	}
	if len(k.held) != 0 {
		t.Errorf("k.held = %v after sendKey failure, want empty map", k.held)
	}
}

// TestSendkeysContext_ModifierClearedOnSendKeyFailure verifies that k.mods is
// restored to its original value when a sendKey fails after temporary modifiers
// were applied. Before the fix, k.mods retained the temporary modifier bits.
func TestSendkeysContext_ModifierClearedOnSendKeyFailure(t *testing.T) {
	// For {ctrl+s}:
	//   write 0: keymap upload
	//   write 1: sendModifiers (apply ctrl)
	//   write 2: sendKey(slot, 1) — we want this to fail
	// Allow 2 writes (keymap + sendModifiers) then fail.
	k, _ := newFailingKeyboard(2)

	actions, err := parseKeySend("{ctrl+s}")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	initialMods := k.mods
	if err := k.sendkeys(context.Background(), actions); err == nil {
		t.Fatal("expected error from injected write failure")
	}
	if k.mods != initialMods {
		t.Errorf("k.mods = %08b after failure, want %08b (should be restored to initial)", k.mods, initialMods)
	}
}

func TestSendkeys_PreservesAlreadyHeldTemporaryModifier(t *testing.T) {
	k, rc := newTestKeyboard()
	k.mods = modControl

	if err := k.sendkeys(context.Background(), []keySend{{key: "a", modifiers: modifiers{ctrl: true}}}); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	if k.mods != modControl {
		t.Fatalf("k.mods = 0x%x, want existing control modifier 0x%x preserved", k.mods, modControl)
	}
	// The keymap upload restores the existing modifier, then the key press and
	// release run without a redundant temporary-modifier apply/clear pair.
	if rc.writes != 4 {
		t.Fatalf("writes = %d, want keymap, modifier restore, key down, key up", rc.writes)
	}
}

type uncertainTempModifiersWire struct {
	recordingCtx
	pressErr    error
	releaseErr  error
	cancel      context.CancelFunc
	failCleanup bool
}

func (w *uncertainTempModifiersWire) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.WriteMsg(data, oob); err != nil {
		return err
	}
	if w.writes == 1 {
		if w.cancel != nil {
			w.cancel()
		}
		return w.pressErr
	}
	if w.failCleanup {
		return w.releaseErr
	}
	return nil
}

// A modifier-state write may reach the compositor before the wire reports a
// failure. Restoring only the local mask leaves a stuck Shift in the compositor.
func TestWaylandFailedTemporaryModifierUpdateCompensatesAtWire(t *testing.T) { //nolint:gocyclo // Compares state and error contracts across four wire-failure combinations.
	for _, tc := range []struct {
		name                 string
		held                 uint32
		cancel, cleanupFails bool
	}{
		{name: "no preexisting modifiers"},
		{name: "preserve preexisting Ctrl", held: modControl},
		{name: "cancel after uncertain write", cancel: true},
		{name: "cleanup also fails", held: modControl, cleanupFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("modifier update delivered before transport error")
			releaseErr := errors.New("modifier restore transport error")
			wire := &uncertainTempModifiersWire{pressErr: pressErr, releaseErr: releaseErr, failCleanup: tc.cleanupFails}
			if tc.cancel {
				wire.cancel = cancel
			}
			k, _ := newTestKeyboard()
			k.ctx = wire
			k.mods = tc.held
			applied, err := k.applyTempMods(ctx, modShift)
			if applied != 0 || !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.cleanupFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("failed modifier apply=%d error=%v", applied, err)
			}
			if k.mods != tc.held {
				t.Fatalf("local modifier state %d, want %d", k.mods, tc.held)
			}
			if wire.writes != 2 {
				t.Fatalf("uncertain modifier update did not receive compensating wire message: %d", wire.writes)
			}
			events := keyboardEventsFromMessages(t, wire.msgs)
			if len(events) != 2 || events[0].opcode != 2 || events[0].state != tc.held|modShift || events[1].opcode != 2 || events[1].state != tc.held {
				t.Fatalf("modifier wire state not restored: %+v", events)
			}
		})
	}
}

var _ wl.Ctx = (*uncertainTempModifiersWire)(nil)
