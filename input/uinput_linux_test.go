//go:build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"github.com/bendahl/uinput"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"github.com/nskaggs/perfuncted/v2/output"
	"math"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type failDirectKeyDown struct {
	*recordingKeyboard
	downErr error
}

func (f *failDirectKeyDown) KeyDown(key int) error {
	if f.downErr != nil {
		return f.downErr
	}
	return f.recordingKeyboard.KeyDown(key)
}

var _ uinput.Keyboard = (*failDirectKeyDown)(nil)

// Failed device writes must not make the backend claim ownership of a key it
// never pressed or relinquish one for which release was not confirmed.
func TestUinputDirectKeyFailuresPreserveCallerOwnedState(t *testing.T) {
	ctx := context.Background()
	t.Run("failed press is not held", func(t *testing.T) {
		failure := errors.New("synthetic keyboard press failure")
		device := &failDirectKeyDown{recordingKeyboard: &recordingKeyboard{}, downErr: failure}
		b := &UinputBackend{kb: device, charToRune: qwertyRuneMap()}
		if err := b.KeyDown(ctx, "ctrl"); !errors.Is(err, failure) {
			t.Fatalf("failed keyboard press returned %v", err)
		}
		if b.keyHeld(uinput.KeyLeftctrl) || len(device.events) != 0 {
			t.Fatalf("failed device write created keyboard ownership: %+v", device.events)
		}
		device.downErr = nil
		if err := b.KeyDown(ctx, "ctrl"); err != nil {
			t.Fatalf("retry press: %v", err)
		}
		if !b.keyHeld(uinput.KeyLeftctrl) {
			t.Fatal("successful retry did not establish hold")
		}
		if err := b.KeyUp(ctx, "ctrl"); err != nil {
			t.Fatalf("release: %v", err)
		}
	})
	t.Run("failed release remains owned", func(t *testing.T) {
		failure := errors.New("synthetic keyboard release failure")
		device := &failingKeyboard{failUp: true, failUpKey: uinput.KeyA, upErr: failure}
		b := &UinputBackend{kb: device, charToRune: qwertyRuneMap()}
		if err := b.KeyDown(ctx, "a"); err != nil {
			t.Fatalf("press: %v", err)
		}
		if err := b.KeyUp(ctx, "a"); !errors.Is(err, failure) {
			t.Fatalf("failed keyboard release returned %v", err)
		}
		if !b.keyHeld(uinput.KeyA) {
			t.Fatal("failed release dropped caller's key hold")
		}
		if err := b.KeyDown(ctx, "a"); err == nil {
			t.Fatal("failed release permitted duplicate key-down")
		}
		device.failUp = false
		if err := b.KeyUp(ctx, "a"); err != nil {
			t.Fatalf("retry release: %v", err)
		}
		if b.keyHeld(uinput.KeyA) {
			t.Fatal("successful release still held")
		}
	})
}

type uinputCancelOnKeyboardPress struct {
	*recordingKeyboard
	cancel context.CancelFunc
}

func (k *uinputCancelOnKeyboardPress) KeyPress(key int) error {
	err := k.recordingKeyboard.KeyPress(key)
	k.cancel()
	return err
}

var _ uinput.Keyboard = (*uinputCancelOnKeyboardPress)(nil)

type cancelOnTouchMove struct {
	*recordingTouchPad
	cancel context.CancelFunc
}

func (t *cancelOnTouchMove) MoveTo(x, y int32) error {
	err := t.recordingTouchPad.MoveTo(x, y)
	t.cancel()
	return err
}

var _ uinput.TouchPad = (*cancelOnTouchMove)(nil)

type cancelOnWheel struct {
	*recordingMouse
	cancel context.CancelFunc
}

func (m *cancelOnWheel) Wheel(horizontal bool, delta int32) error {
	err := m.recordingMouse.Wheel(horizontal, delta)
	m.cancel()
	return err
}

var _ uinput.Mouse = (*cancelOnWheel)(nil)

// A synthetic device can finish its wire write just as an operation is
// cancelled. Its successful return does not make the cancelled request succeed.
func TestUinputCompletedDeviceWriteCannotHideLateCallerCancellation(t *testing.T) {
	t.Run("literal keyboard", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		kb := &uinputCancelOnKeyboardPress{recordingKeyboard: &recordingKeyboard{}, cancel: cancel}
		b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
		if err := b.TypeLiteral(ctx, "a"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled keyboard write reported %v", err)
		}
		if len(kb.events) != 1 {
			t.Fatalf("device did not receive one real key write: %v", kb.events)
		}
	})
	t.Run("absolute move", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		tp := &cancelOnTouchMove{recordingTouchPad: &recordingTouchPad{}, cancel: cancel}
		b := &UinputBackend{touchpad: tp}
		if err := b.MouseMove(ctx, 45, 61); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled move reported %v", err)
		}
		if len(tp.events) != 1 {
			t.Fatalf("device did not receive one move: %v", tp.events)
		}
	})
	t.Run("scroll wheel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		mouse := &cancelOnWheel{recordingMouse: &recordingMouse{}, cancel: cancel}
		b := &UinputBackend{mouse: mouse}
		if err := b.ScrollDown(ctx, 2); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled scroll reported %v", err)
		}
		if events := mouse.eventsSnapshot(); len(events) != 1 || events[0] != "wheel:false:2" {
			t.Fatalf("device write not observed: %v", events)
		}
	})
}

// A pointer device covering W logical pixels must expose axis positions 0..W-1.
// Advertising 0..W makes KWin map the requested center and right half to the
// previous pixel, turning verified relative moves into false failures.
func TestUinputTouchpadAxesUseLastLogicalPixel(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int32
	}{
		{name: "normal", width: 1600, height: 900},
		{name: "fractional-scaled-real-kde", width: 2867, height: 1200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withUinputSyntheticConstructors(t,
				func(string) (os.FileInfo, error) { return nil, nil },
				func(string, []byte) (uinput.Keyboard, error) { return &constructorRecordingKeyboard{}, nil },
				func(_ string, _ []byte, minX, maxX, minY, maxY int32) (uinput.TouchPad, error) {
					if minX != 0 || minY != 0 || maxX != tc.width-1 || maxY != tc.height-1 {
						t.Fatalf("touchpad bounds (%d..%d,%d..%d) do not match last logical pixel (%d,%d)",
							minX, maxX, minY, maxY, tc.width-1, tc.height-1)
					}
					return &recordingTouchPad{}, nil
				},
				func() (map[rune]kernelChar, error) { return qwertyRuneMap(), nil },
			)
			b, err := NewUinputBackend(tc.width, tc.height)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Reproduces the KDE report: perfuncted-touchpad: kernel bug: device has min == max on ABS_X.
// Validation must happen before opening any host device.
func TestUinputRejectsInvalidTouchpadDimensionsBeforeDeviceCreation(t *testing.T) {
	for _, bounds := range [][2]int32{{0, 0}, {0, 900}, {1600, 0}, {-1, 900}, {1600, -1}, {1, 900}, {1600, 1}} {
		_, err := NewUinputBackend(bounds[0], bounds[1])
		if err == nil || !strings.Contains(err.Error(), "dimensions") {
			t.Errorf("bounds %v: wanted dimension error before touching /dev/uinput, got %v", bounds, err)
		}
	}
}

func TestUinputBoundsFromLogicalOutputs(t *testing.T) {
	tests := []struct {
		name         string
		outputs      []output.Info
		wantX, wantY int32
		wantErr      bool
	}{
		{name: "fractional scale uses logical output", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080, ScaleNumerator: 6, ScaleDenominator: 5}}, wantX: 1600, wantY: 900},
		{name: "dual displays", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}}, {Available: true, Geometry: output.Geometry{X: 1600, W: 1280, H: 1024}}}, wantX: 2880, wantY: 1024},
		{name: "negative offset fails closed", outputs: []output.Info{{Available: true, Geometry: output.Geometry{X: -100, W: 1600, H: 900}}}, wantErr: true},
		{name: "missing dimensions fail", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 0, H: 900}}}, wantErr: true},
		{name: "no outputs fail", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x, y, err := uinputBoundsFromOutputs(tt.outputs)
			if (err != nil) != tt.wantErr || (!tt.wantErr && (x != tt.wantX || y != tt.wantY)) {
				t.Fatalf("got (%d,%d) err=%v, want (%d,%d) err?%t", x, y, err, tt.wantX, tt.wantY, tt.wantErr)
			}
		})
	}
}

// Probe missing devices and canceled operations at the actual public method
// boundary without requiring /dev/uinput or a graphical session.
func TestUinputCanceledAndIncompleteBackendNeverInjectsEvents(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var b *UinputBackend
		if err := b.KeyDown(context.Background(), "ctrl"); err == nil {
			t.Fatal("nil backend injected keyboard event")
		}
		if _, err := b.PointerCoordinateSpace(context.Background()); !errors.Is(err, ErrNotSupported) {
			t.Fatalf("nil backend fabricated logical mapping: %v", err)
		}
	})
	t.Run("canceled authorized coordinate query", func(t *testing.T) {
		b, _ := newTestBackend(t)
		b.verifiedSingleLogicalOutput = true
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := b.PointerCoordinateSpace(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled pointer query returned %v", err)
		}
		if space, err := b.PointerCoordinateSpace(context.Background()); err != nil || space.Kind != CoordinateSpaceLogical {
			t.Fatalf("authorized logical coordinate space=%+v err=%v", space, err)
		}
	})
	t.Run("missing keyboard device", func(t *testing.T) {
		b := &UinputBackend{charToRune: qwertyRuneMap()}
		for _, tc := range []struct {
			name string
			call func() error
		}{
			{"key down", func() error { return b.KeyDown(context.Background(), "ctrl") }},
			{"key up", func() error { return b.KeyUp(context.Background(), "ctrl") }},
			{"literal", func() error { return b.TypeLiteral(context.Background(), "private") }},
			{"parsed", func() error { return b.Type(context.Background(), "private{enter}") }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if err := tc.call(); !errors.Is(err, ErrNotSupported) {
					t.Fatalf("missing uinput keyboard accepted command: %v", err)
				}
			})
		}
	})
	t.Run("cancel all input actions", func(t *testing.T) {
		b, kb := newTestBackend(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		for _, tc := range []struct {
			name string
			call func() error
		}{
			{"key down", func() error { return b.KeyDown(ctx, "ctrl") }},
			{"key up", func() error { return b.KeyUp(ctx, "ctrl") }},
			{"type", func() error { return b.Type(ctx, "private{enter}") }},
			{"literal", func() error { return b.TypeLiteral(ctx, "private") }},
			{"mouse down", func() error { return b.MouseDown(ctx, 1) }},
			{"mouse up", func() error { return b.MouseUp(ctx, 1) }},
			{"scroll right", func() error { return b.ScrollRight(ctx, 1) }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if err := tc.call(); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled input action returned %v", err)
				}
			})
		}
		if len(kb.events) != 0 {
			t.Fatalf("canceled input emitted keyboard events: %v", kb.events)
		}
	})
}

// Middle-click needs a separately acquired uinput mouse. A failed lazy
// creation must not move the pointer before reporting an unavailable button.
func TestUinputMiddleClickDoesNotMoveBeforeMouseDeviceAcquisition(t *testing.T) {
	b, tp, _ := newUinputActionBackend()
	b.mouse = nil
	unavailable := errors.New("synthetic /dev/uinput denied")
	old := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) { return nil, unavailable }
	t.Cleanup(func() { createUinputMouse = old })
	err := b.MouseClick(context.Background(), 30, 40, 2)
	if !errors.Is(err, unavailable) {
		t.Fatalf("unavailable middle mouse returned %v", err)
	}
	if len(tp.events) != 0 {
		t.Fatalf("click moved pointer before discovering unavailable middle-button device: %v", tp.events)
	}
}

func TestUinputMiddleClickWithAcquiredMousePreservesMotionPressRelease(t *testing.T) {
	b, tp, mouse := newUinputActionBackend()
	if err := b.MouseClick(context.Background(), 30, 40, 2); err != nil {
		t.Fatal(err)
	}
	if got := tp.events; len(got) != 1 || got[0] != "move:30,40" {
		t.Fatalf("middle click motion=%v", got)
	}
	if got := mouse.eventsSnapshot(); len(got) != 2 || got[0] != "middle-press" || got[1] != "middle-release" {
		t.Fatalf("middle click events=%v", got)
	}
}

type failingUinputClickReleaseTouchpad struct {
	*cancelingTouchPad
	releaseErr error
}

func (p *failingUinputClickReleaseTouchpad) LeftRelease() error {
	p.record("left-release")
	return p.releaseErr
}

// The release must be attempted even after the operation loses its context.
// The caller needs both the cancellation and the failed cleanup to decide
// whether the physical button may still be pressed on the input device.
func TestUinputClickPreservesCancellationAndButtonReleaseFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	releaseFailure := errors.New("synthetic uinput button release failed")
	touchpad := &failingUinputClickReleaseTouchpad{
		cancelingTouchPad: &cancelingTouchPad{cancel: cancel},
		releaseErr:        releaseFailure,
	}
	backend := &UinputBackend{touchpad: touchpad}
	err := backend.MouseClick(ctx, 18, 27, 1)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, releaseFailure) {
		t.Fatalf("canceled click lost cancellation or release failure: %v", err)
	}
	want := []string{"move:18,27", "left-press", "left-release"}
	if !slices.Equal(touchpad.events, want) {
		t.Fatalf("canceled click device writes = %v, want %v", touchpad.events, want)
	}
	if closeErr := backend.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}

func TestUinputClosedBackendNeverStartsLazyMouseFactory(t *testing.T) {
	backend := &UinputBackend{}
	if err := backend.Close(); err != nil {
		t.Fatalf("close empty backend: %v", err)
	}

	created := false
	oldFactory := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) {
		created = true
		return &recordingMouse{}, nil
	}
	t.Cleanup(func() { createUinputMouse = oldFactory })

	if err := backend.ensureMouseLocked(); !errors.Is(err, errUinputBackendClosed) {
		t.Fatalf("closed backend lazy mouse result = %v, want closed error", err)
	}
	if created || backend.mouse != nil {
		t.Fatalf("closed backend created or published a lazy mouse: called=%v mouse=%v", created, backend.mouse)
	}
}

func withUinputSyntheticConstructors(t *testing.T, stat func(string) (os.FileInfo, error), keyboard func(string, []byte) (uinput.Keyboard, error), touchpad func(string, []byte, int32, int32, int32, int32) (uinput.TouchPad, error), keymap func() (map[rune]kernelChar, error)) {
	t.Helper()
	oldStat, oldKB, oldTP, oldMap := statUinputDevice, createUinputKeyboard, createUinputTouchpad, readUinputKernelMap
	statUinputDevice, createUinputKeyboard, createUinputTouchpad, readUinputKernelMap = stat, keyboard, touchpad, keymap
	t.Cleanup(func() {
		statUinputDevice, createUinputKeyboard, createUinputTouchpad, readUinputKernelMap = oldStat, oldKB, oldTP, oldMap
	})
}

// Cover the real exported constructor and its ownership contract without
// touching host devices, udev permissions, or a kernel virtual console.
func TestUinputConstructorFactoryAndKeymapFailureMatrix(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		statErr, kbErr, tpErr, keymapErr error
		want                             string
		kbCreated, tpCreated, closedKB   bool
	}{
		{name: "device missing", statErr: os.ErrNotExist, want: "not found"},
		{name: "keyboard permission denied", kbErr: os.ErrPermission, want: "permission denied", kbCreated: true},
		{name: "keyboard creation failure", kbErr: errors.New("keyboard ioctl refused"), want: "create keyboard", kbCreated: true},
		{name: "touchpad creation failure", tpErr: errors.New("touchpad ioctl refused"), want: "create touchpad", kbCreated: true, tpCreated: true, closedKB: true},
		{name: "kernel keymap missing uses fallback", keymapErr: errors.New("virtual console unavailable"), kbCreated: true, tpCreated: true},
		{name: "kernel keymap available", kbCreated: true, tpCreated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyboardCalls, touchpadCalls := 0, 0
			kb := &constructorRecordingKeyboard{}
			tp := &recordingTouchPad{}
			withUinputSyntheticConstructors(t,
				func(path string) (os.FileInfo, error) {
					if path != "/dev/uinput" {
						t.Fatalf("wrong device %s", path)
					}
					return nil, tc.statErr
				},
				func(path string, name []byte) (uinput.Keyboard, error) {
					keyboardCalls++
					if path != "/dev/uinput" || string(name) != "perfuncted-keyboard" {
						t.Fatalf("unexpected keyboard %s/%s", path, name)
					}
					if tc.kbErr != nil {
						return nil, tc.kbErr
					}
					return kb, nil
				},
				func(path string, name []byte, xmin, xmax, ymin, ymax int32) (uinput.TouchPad, error) {
					touchpadCalls++
					if path != "/dev/uinput" || string(name) != "perfuncted-touchpad" || xmin != 0 || xmax != 1439 || ymin != 0 || ymax != 899 {
						t.Fatalf("unexpected touchpad dimensions %d,%d,%d,%d", xmin, xmax, ymin, ymax)
					}
					if tc.tpErr != nil {
						return nil, tc.tpErr
					}
					return tp, nil
				},
				func() (map[rune]kernelChar, error) { return map[rune]kernelChar{'z': {keycode: 12}}, tc.keymapErr },
			)
			backend, err := NewUinputBackend(1440, 900)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || backend != nil {
					t.Fatalf("factory failure got backend=%v error=%v", backend, err)
				}
			} else {
				if err != nil || backend == nil {
					t.Fatalf("valid synthetic backend = %v err=%v", backend, err)
				}
				if tc.keymapErr != nil {
					if _, ok := backend.charToRune['a']; !ok {
						t.Fatal("kernel-keymap failure did not use QWERTY fallback")
					}
				} else {
					if _, ok := backend.charToRune['z']; !ok {
						t.Fatal("kernel keymap success was ignored")
					}
				}
				if err := backend.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if (keyboardCalls != 0) != tc.kbCreated || (touchpadCalls != 0) != tc.tpCreated {
				t.Fatalf("factory calls keyboard=%d touchpad=%d", keyboardCalls, touchpadCalls)
			}
			if tc.closedKB && kb.closeCount != 1 {
				t.Fatalf("touchpad failure did not close acquired keyboard: %d", kb.closeCount)
			}
		})
	}
}

type constructorRecordingKeyboard struct {
	recordingKeyboard
	closeCount int
	failure    error
}

func (k *constructorRecordingKeyboard) Close() error { k.closeCount++; return k.failure }

// If rollback itself fails, that error must not be discarded: the caller is
// responsible for understanding whether the virtual device remained owned.
func TestUinputConstructorReportsTouchpadAndKeyboardRollbackFailures(t *testing.T) {
	touchErr := errors.New("synthetic touchpad rejected")
	closeErr := errors.New("synthetic keyboard close rejected")
	kb := &constructorRecordingKeyboard{failure: closeErr}
	withUinputSyntheticConstructors(t,
		func(string) (os.FileInfo, error) { return nil, nil },
		func(string, []byte) (uinput.Keyboard, error) { return kb, nil },
		func(string, []byte, int32, int32, int32, int32) (uinput.TouchPad, error) { return nil, touchErr },
		func() (map[rune]kernelChar, error) { return nil, fmt.Errorf("should not build keymap") },
	)
	backend, err := NewUinputBackend(800, 600)
	if backend != nil || !errors.Is(err, touchErr) || !errors.Is(err, closeErr) {
		t.Fatalf("constructor lost ownership cleanup failure: backend=%v err=%v", backend, err)
	}
	if kb.closeCount != 1 {
		t.Fatalf("keyboard rollback count=%d, want exactly one", kb.closeCount)
	}
}

func TestUinputConstructorRejectsNilDevicesBeforePublishingUsableBackend(t *testing.T) {
	for _, tc := range []struct {
		name        string
		nilKeyboard bool
	}{
		{name: "nil keyboard", nilKeyboard: true},
		{name: "nil touchpad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kb := &constructorRecordingKeyboard{}
			touchpadCalls := 0
			withUinputSyntheticConstructors(t,
				func(string) (os.FileInfo, error) { return nil, nil },
				func(string, []byte) (uinput.Keyboard, error) {
					if tc.nilKeyboard {
						return nil, nil
					}
					return kb, nil
				},
				func(string, []byte, int32, int32, int32, int32) (uinput.TouchPad, error) {
					touchpadCalls++
					return nil, nil
				},
				func() (map[rune]kernelChar, error) { return qwertyRuneMap(), nil },
			)
			backend, err := NewUinputBackend(1024, 768)
			if err == nil || backend != nil {
				t.Fatalf("nil device exposed as usable backend=%v error=%v", backend, err)
			}
			if tc.nilKeyboard && touchpadCalls != 0 {
				t.Fatalf("touchpad constructor called after nil keyboard")
			}
			if !tc.nilKeyboard && kb.closeCount != 1 {
				t.Fatalf("nil touchpad did not close acquired keyboard; closes=%d", kb.closeCount)
			}
		})
	}
}

func TestUinputConstructorClosesPartiallyReturnedDevicesOnFactoryError(t *testing.T) {
	firstErr := errors.New("synthetic partial construction failure")
	cleanupErr := errors.New("synthetic partial device cleanup failure")
	for _, tc := range []struct {
		name            string
		keyboardPartial bool
	}{
		{name: "partially created keyboard", keyboardPartial: true},
		{name: "partially created touchpad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kb := &constructorRecordingKeyboard{}
			tp := &recordingTouchPad{closeErr: cleanupErr}
			if tc.keyboardPartial {
				kb.failure = cleanupErr
			}
			touchpadCalls := 0
			withUinputSyntheticConstructors(t,
				func(string) (os.FileInfo, error) { return nil, nil },
				func(string, []byte) (uinput.Keyboard, error) {
					if tc.keyboardPartial {
						return kb, firstErr
					}
					return kb, nil
				},
				func(string, []byte, int32, int32, int32, int32) (uinput.TouchPad, error) {
					touchpadCalls++
					return tp, firstErr
				},
				func() (map[rune]kernelChar, error) {
					t.Fatal("keymap loaded after failed constructor")
					return nil, nil
				},
			)
			result, err := NewUinputBackend(1280, 720)
			if result != nil || !errors.Is(err, firstErr) || !errors.Is(err, cleanupErr) {
				t.Fatalf("partial uinput construction leaked original or cleanup error: backend=%v err=%v", result, err)
			}
			if kb.closeCount != 1 {
				t.Fatalf("keyboard cleanup count=%d", kb.closeCount)
			}
			if tc.keyboardPartial {
				if touchpadCalls != 0 {
					t.Fatal("touchpad created after keyboard factory failure")
				}
			} else if got := tp.events; len(got) != 1 || got[0] != "close" {
				t.Fatalf("partially created touchpad was never released: %v", got)
			}
		})
	}
}

// Coordinate authority belongs to the live virtual input backend, not to
// constructor metadata left behind after device teardown. A closed backend
// must never report an active coordinate mapping.
func TestUinputClosedBackendDoesNotAdvertiseCoordinateAuthority(t *testing.T) {
	b := &UinputBackend{verifiedSingleLogicalOutput: true}
	before, err := b.PointerCoordinateSpace(context.Background())
	if err != nil || before.Kind != CoordinateSpaceLogical {
		t.Fatalf("live verified backend: %+v %v", before, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := b.PointerCoordinateSpace(context.Background())
	if after != (CoordinateSpaceInfo{}) || !errors.Is(err, errUinputBackendClosed) {
		t.Fatalf("closed backend retained authority: %+v %v", after, err)
	}
}

// The synthetic uinput devices provide real evidence of events at the evdev
// interface boundary; horizontal and vertical scroll directions cannot be
// validated by merely checking for a nil error.
func TestUinputAllPointerButtonsAndScrollDirectionsProduceExpectedDeviceEvents(t *testing.T) {
	b, tp, mouse := newUinputActionBackend()
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		call  func() error
		tp    []string
		mouse []string
	}{
		{"left button down", func() error { return b.MouseDown(ctx, 1) }, []string{"left-press"}, nil},
		{"left button up", func() error { return b.MouseUp(ctx, 1) }, []string{"left-release"}, nil},
		{"right button down", func() error { return b.MouseDown(ctx, 3) }, []string{"right-press"}, nil},
		{"right button up", func() error { return b.MouseUp(ctx, 3) }, []string{"right-release"}, nil},
		{"middle button down", func() error { return b.MouseDown(ctx, 2) }, nil, []string{"middle-press"}},
		{"middle button up", func() error { return b.MouseUp(ctx, 2) }, nil, []string{"middle-release"}},
		{"logical move", func() error { return b.MouseMove(ctx, 15, 42) }, []string{"move:15,42"}, nil},
		{"scroll up", func() error { return b.ScrollUp(ctx, 3) }, nil, []string{"wheel:false:-3"}},
		{"scroll down", func() error { return b.ScrollDown(ctx, 2) }, nil, []string{"wheel:false:2"}},
		{"scroll left", func() error { return b.ScrollLeft(ctx, 5) }, nil, []string{"wheel:true:-5"}},
		{"scroll right", func() error { return b.ScrollRight(ctx, 7) }, nil, []string{"wheel:true:7"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforeTouch, beforeMouse := len(tp.events), len(mouse.eventsSnapshot())
			if err := tc.call(); err != nil {
				t.Fatal(err)
			}
			if actual := tp.events[beforeTouch:]; !slices.Equal(actual, tc.tp) {
				t.Fatalf("touchpad emitted %v, want %v", actual, tc.tp)
			}
			if actual := mouse.eventsSnapshot()[beforeMouse:]; !slices.Equal(actual, tc.mouse) {
				t.Fatalf("mouse emitted %v, want %v", actual, tc.mouse)
			}
		})
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if got := mouse.eventsSnapshot(); got[len(got)-1] != "close" {
		t.Fatalf("mouse was not closed: %v", got)
	}
}

func TestUinputExplicitModifierAndLiteralTypingPreserveKeyboardState(t *testing.T) {
	b, kb := newTestBackend(t)
	ctx := context.Background()
	if err := b.Type(ctx, "{super down}{super+a}{super up}"); err != nil {
		t.Fatal(err)
	}
	if b.modifierHeld(uinput.KeyLeftmeta) {
		t.Fatal("held super not released by explicit up")
	}
	want := []string{fmt.Sprintf("down:%d", uinput.KeyLeftmeta), fmt.Sprintf("press:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyLeftmeta)}
	if !slices.Equal(kb.events, want) {
		t.Fatalf("modifier and key wire order %v, want %v", kb.events, want)
	}
	if err := b.TypeLiteral(ctx, "Aa"); err != nil {
		t.Fatal(err)
	}
	tail := kb.events[len(want):]
	literalWant := []string{fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("press:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyLeftshift), fmt.Sprintf("press:%d", uinput.KeyA)}
	if !slices.Equal(tail, literalWant) {
		t.Fatalf("literal text event order %v, want %v", tail, literalWant)
	}
}

func TestUinputUnavailablePointerDoesNotReportLocation(t *testing.T) {
	b, _, _ := newUinputActionBackend()
	x, y, err := b.PointerLocation(context.Background())
	if x != 0 || y != 0 || !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unavailable pointer reported %d,%d %v", x, y, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err = b.PointerLocation(context.Background())
	if !errors.Is(err, errUinputBackendClosed) {
		t.Fatalf("closed pointer backend error %v", err)
	}
}

// A failed device probe is not proof a privileged virtual input endpoint is
// usable. Never attempt to create devices after an unclassified stat error.
func TestUinputConstructorRefusesUnverifiableDeviceBeforeCreation(t *testing.T) {
	for _, failure := range []error{os.ErrPermission, errors.New("synthetic uinput device I/O failure")} {
		t.Run(failure.Error(), func(t *testing.T) {
			keyboardCalls, touchpadCalls := 0, 0
			withUinputSyntheticConstructors(t,
				func(string) (os.FileInfo, error) { return nil, failure },
				func(string, []byte) (uinput.Keyboard, error) {
					keyboardCalls++
					return nil, errors.New("unexpected keyboard creation")
				},
				func(string, []byte, int32, int32, int32, int32) (uinput.TouchPad, error) {
					touchpadCalls++
					return nil, errors.New("unexpected touchpad creation")
				},
				func() (map[rune]kernelChar, error) { t.Fatal("keymap read after failed device probe"); return nil, nil },
			)
			backend, err := NewUinputBackend(800, 600)
			if backend != nil || !errors.Is(err, failure) || keyboardCalls != 0 || touchpadCalls != 0 {
				t.Fatalf("probe error %v gave backend=%v err=%v keyboardCalls=%d touchpadCalls=%d", failure, backend, err, keyboardCalls, touchpadCalls)
			}
		})
	}
}

func TestUinputTypeExplicitKeyWriteFailuresPreserveOwnership(t *testing.T) {
	t.Run("failed key-down does not create a hold", func(t *testing.T) {
		failure := errors.New("synthetic key-down failure")
		keyboard := &failDirectKeyDown{recordingKeyboard: &recordingKeyboard{}, downErr: failure}
		backend := &UinputBackend{kb: keyboard, charToRune: qwertyRuneMap()}

		err := backend.Type(context.Background(), "{enter down}")
		if !errors.Is(err, failure) {
			t.Fatalf("Type returned %v, want key-down failure", err)
		}
		if backend.keyHeld(uinput.KeyEnter) || len(keyboard.events) != 0 {
			t.Fatalf("failed key-down created ownership or events: held=%v events=%v", backend.keyHeld(uinput.KeyEnter), keyboard.events)
		}
	})

	t.Run("failed key-up preserves the hold", func(t *testing.T) {
		failure := errors.New("synthetic key-up failure")
		keyboard := &failingKeyboard{failUp: true, failUpKey: uinput.KeyEnter, upErr: failure}
		backend := &UinputBackend{
			kb:         keyboard,
			charToRune: qwertyRuneMap(),
			heldKeys:   map[int]bool{uinput.KeyEnter: true},
		}

		err := backend.Type(context.Background(), "{enter up}")
		if !errors.Is(err, failure) {
			t.Fatalf("Type returned %v, want key-up failure", err)
		}
		want := []string{fmt.Sprintf("up:%d", uinput.KeyEnter)}
		if !backend.keyHeld(uinput.KeyEnter) || !slices.Equal(keyboard.events, want) {
			t.Fatalf("failed key-up changed ownership or emitted unexpected events: held=%v events=%v", backend.keyHeld(uinput.KeyEnter), keyboard.events)
		}
	})
}

func TestUinputDirectKeyOperationsRejectUnknownNamesBeforeEvents(t *testing.T) {
	backend, keyboard := newTestBackend(t)
	for _, operation := range []struct {
		name string
		call func() error
	}{
		{"KeyDown", func() error { return backend.KeyDown(context.Background(), "not-a-real-key") }},
		{"KeyUp", func() error { return backend.KeyUp(context.Background(), "not-a-real-key") }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.call(); err == nil {
				t.Fatal("unknown physical key name was accepted")
			}
			if len(keyboard.events) != 0 || len(backend.heldKeys) != 0 {
				t.Fatalf("invalid key changed backend state: events=%v held=%v", keyboard.events, backend.heldKeys)
			}
		})
	}
}

type cancelingUinputLister struct {
	cancel context.CancelFunc
	closed bool
}

func (l *cancelingUinputLister) List(context.Context) ([]output.Info, error) {
	l.cancel()
	return []output.Info{{Available: true, Geometry: output.Geometry{W: 1920, H: 1080}}}, nil
}
func (l *cancelingUinputLister) Close() error { l.closed = true; return nil }

// Enumeration is not authorization to create a kernel input device after the
// original caller has revoked its operation, even when metadata is valid.
func TestUinputDiscoveryCancellationDoesNotCreateDevice(t *testing.T) {
	oldOpen, oldCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister, newUinputBackend = oldOpen, oldCreate })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lister := &cancelingUinputLister{cancel: cancel}
	openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) { return lister, nil }
	created := 0
	newUinputBackend = func(int32, int32) (Inputter, error) {
		created++
		return noopInputter{}, nil
	}
	backend, err := newResolvedUinputBackend(ctx, env.Runtime{}, 0, 0)
	if !errors.Is(err, context.Canceled) || backend != nil || created != 0 || !lister.closed {
		t.Fatalf("revoked discovery: backend=%T err=%v created=%d closed=%v", backend, err, created, lister.closed)
	}
}

func TestUinputExplicitDimensionsCanceledBeforeDeviceCreation(t *testing.T) {
	oldCreate := newUinputBackend
	t.Cleanup(func() { newUinputBackend = oldCreate })
	created := 0
	newUinputBackend = func(int32, int32) (Inputter, error) { created++; return noopInputter{}, nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend, err := newResolvedUinputBackend(ctx, env.Runtime{}, 1920, 1080)
	if !errors.Is(err, context.Canceled) || backend != nil || created != 0 {
		t.Fatalf("revoked explicit dimensions: backend=%T err=%v created=%d", backend, err, created)
	}
}

// The kernel device may have been opened by the time cancellation arrives.
// It must be closed rather than handed to a caller whose operation has ended.
func TestUinputCancellationDuringConstructorClosesCreatedDevice(t *testing.T) {
	oldCreate := newUinputBackend
	t.Cleanup(func() { newUinputBackend = oldCreate })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	created := &selectorTrackedInputter{}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		cancel()
		return created, nil
	}
	backend, err := newResolvedUinputBackend(ctx, env.Runtime{}, 1920, 1080)
	if !errors.Is(err, context.Canceled) || backend != nil || created.closed != 1 {
		t.Fatalf("constructor canceled: backend=%T err=%v close count=%d", backend, err, created.closed)
	}
}

// A canceled request must not enter a device operation while another
// operation owns the injection gate, even if its backend is still open.
func TestUinputWaitingForOperationGateHonorsCancellation(t *testing.T) {
	backend := &UinputBackend{}
	gate := backend.operationGateChannel()
	<-gate // simulate a device operation currently holding the gate
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- backend.withOperation(ctx, func(context.Context) error {
			entered <- struct{}{}
			return nil
		})
	}()
	// Give the admitted operation a chance to block on the occupied gate.
	// The gate remains unavailable throughout cancellation.
	runtime.Gosched()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting operation returned %v, want context cancellation", err)
	}
	select {
	case <-entered:
		t.Fatal("canceled operation entered device callback")
	default:
	}
	gate <- struct{}{}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUinputNeverTapsHeldModifierWithoutExplicitRelease(t *testing.T) {
	for _, tc := range []struct{ name, initial, command string }{
		{"prior ctrl tap", "ctrl", "{ctrl}"},
		{"prior shift tap after text", "shift", "private{shift}"},
		{"prior alt repeat", "alt", "{alt down}"},
		{"same batch ctrl down then tap", "", "{ctrl down}{ctrl}"},
		{"same batch shift down then repeat", "", "{shift down}{shift down}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, keyboard := newTestBackend(t)
			if tc.initial != "" {
				if err := backend.KeyDown(context.Background(), tc.initial); err != nil {
					t.Fatal(err)
				}
			}
			before := len(keyboard.events)
			err := backend.Type(context.Background(), tc.command)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("uinput accepted tap or repeat of held modifier: %v", err)
			}
			if len(keyboard.events) != before {
				t.Fatalf("denied batch injected %v", keyboard.events[before:])
			}
		})
	}
}

func TestUinputNeverRepeatsHeldModifierKeyDown(t *testing.T) {
	for _, mod := range []string{"ctrl", "shift", "alt", "super"} {
		t.Run(mod, func(t *testing.T) {
			backend, keyboard := newTestBackend(t)
			if err := backend.KeyDown(context.Background(), mod); err != nil {
				t.Fatal(err)
			}
			before := len(keyboard.events)
			err := backend.KeyDown(context.Background(), mod)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("uinput repeated held modifier: %v", err)
			}
			if len(keyboard.events) != before {
				t.Fatalf("duplicate down injected %v", keyboard.events[before:])
			}
			if err := backend.KeyUp(context.Background(), mod); err != nil {
				t.Fatalf("original modifier cannot release: %v", err)
			}
		})
	}
}

// A tap releases its key; it cannot be allowed to consume an ordinary key
// that this same batch just placed into a persistent hold. Reject the whole
// command before emitting any prefix that could modify the focused target.
func TestUinputBatchRejectsTappingOrdinaryKeyHeldEarlierInCommand(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"tap after down", "{a down}{a}"},
		{"tap after safe prefix", "safe{enter down}{enter}"},
		{"named tap after separate hold", "{f5 down}{f5}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, kb := newTestBackend(t)
			err := backend.Type(context.Background(), tc.command)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("unsafe release-through-tap accepted: command=%q err=%v", tc.command, err)
			}
			if len(kb.events) != 0 {
				t.Fatalf("invalid batch injected prefix events: %v", kb.events)
			}
			if len(backend.heldKeys) != 0 {
				t.Fatalf("invalid batch retained phantom held keys: %v", backend.heldKeys)
			}
		})
	}
	backend, kb := newTestBackend(t)
	if err := backend.Type(context.Background(), "{a down}{a up}{a}"); err != nil {
		t.Fatalf("valid explicit release before tap rejected: %v", err)
	}
	if len(kb.events) != 3 || len(backend.heldKeys) != 0 {
		t.Fatalf("valid release-before-tap did not complete or leaked holds: events=%v holds=%v", kb.events, backend.heldKeys)
	}
}

func TestUinputLogicalBoundsIgnoreUnavailableOutputsAndRejectOverflow(t *testing.T) {
	width, height, err := uinputBoundsFromOutputs([]output.Info{
		{Geometry: output.Geometry{X: -500, Y: -300, W: 9000, H: 9000}},
		{Available: true, Geometry: output.Geometry{X: 10, Y: 20, W: 1920, H: 1080}},
	})
	if err != nil || width != 1930 || height != 1100 {
		t.Fatalf("logical bounds = %dx%d, %v; want 1930x1100", width, height, err)
	}

	_, _, err = uinputBoundsFromOutputs([]output.Info{{
		Available: true,
		Geometry:  output.Geometry{X: math.MaxInt32 - 10, W: 20, H: 1080},
	}})
	if err == nil || !strings.Contains(err.Error(), "exceeds input device range") {
		t.Fatalf("overflowing output geometry accepted: %v", err)
	}
}

func TestUinputLogicalBoundsDiscoveryPreservesCancellationAndOpenFailure(t *testing.T) {
	oldOpen := openUinputOutputLister
	t.Cleanup(func() { openUinputOutputLister = oldOpen })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opened := false
	openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) {
		opened = true
		return nil, nil
	}
	if _, _, _, err := discoverUinputLogicalBounds(ctx, env.Runtime{}); !errors.Is(err, context.Canceled) || opened {
		t.Fatalf("pre-canceled discovery opened lister=%t err=%v", opened, err)
	}

	openFailure := errors.New("synthetic output discovery failure")
	openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) {
		return nil, openFailure
	}
	oldCreate := newUinputBackend
	newUinputBackend = func(int32, int32) (Inputter, error) {
		t.Fatal("output discovery failure reached device creation")
		return nil, nil
	}
	t.Cleanup(func() { newUinputBackend = oldCreate })
	backend, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 0, 0)
	if backend != nil || !errors.Is(err, openFailure) {
		t.Fatalf("failed output discovery published backend=%T err=%v", backend, err)
	}
}

func TestUinputResolvedBackendRejectsInvalidDimensionsBeforeDeviceCreation(t *testing.T) {
	oldCreate := newUinputBackend
	t.Cleanup(func() { newUinputBackend = oldCreate })
	newUinputBackend = func(int32, int32) (Inputter, error) {
		t.Fatal("invalid touchpad dimensions reached device creation")
		return nil, nil
	}
	backend, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 1, 1080)
	if backend != nil || err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("invalid dimensions returned backend=%T err=%v", backend, err)
	}
}

// Only real modifier keycodes may change the modifier ownership bitmask.
// In particular, ordinary keys must not clear a modifier held by the caller.
func TestUinputModifierBookkeepingIgnoresOrdinaryKeysAndKeepsSidesIndependent(t *testing.T) {
	backend := &UinputBackend{}
	backend.markModifierHeld(uinput.KeyLeftshift)
	backend.markModifierHeld(uinput.KeyRightshift)
	backend.markModifierHeld(uinput.KeyLeftctrl)
	initial := backend.heldMods
	if initial == 0 {
		t.Fatal("modifier holds not registered")
	}
	for _, code := range []int{uinput.KeyA, uinput.KeySpace, -1, 0} {
		if backend.modifierHeld(code) {
			t.Fatalf("ordinary keycode %d reported as a held modifier", code)
		}
		backend.markModifierHeld(code)
		if backend.heldMods != initial {
			t.Fatalf("ordinary keycode %d changed modifier ownership: got %b want %b", code, backend.heldMods, initial)
		}
		backend.markModifierReleased(code)
		if backend.heldMods != initial {
			t.Fatalf("ordinary keycode %d released a caller-owned modifier", code)
		}
	}
	backend.markModifierReleased(uinput.KeyLeftshift)
	if backend.modifierHeld(uinput.KeyLeftshift) || !backend.modifierHeld(uinput.KeyRightshift) || !backend.modifierHeld(uinput.KeyLeftctrl) {
		t.Fatal("releasing left shift changed unrelated modifier ownership")
	}
	backend.markModifierReleased(uinput.KeyRightshift)
	backend.markModifierReleased(uinput.KeyLeftctrl)
	if backend.heldMods != 0 {
		t.Fatalf("released modifiers still held: %b", backend.heldMods)
	}
}

// Close on an absent backend is safe during partially failed initialization.
func TestNilUinputBackendCloseIsSafe(t *testing.T) {
	var backend *UinputBackend
	if err := backend.Close(); err != nil {
		t.Fatalf("nil backend Close: %v", err)
	}
}

// Shutdown can win while the lazy mouse factory is executing. Its acquired
// device must be rolled back, and a failed rollback must remain observable.
func TestUinputLazyMouseFactoryShutdownRetainsCleanupFailure(t *testing.T) {
	b := &UinputBackend{}
	cleanupErr := errors.New("synthetic late mouse close failure")
	mouse := &recordingMouse{closeErr: cleanupErr}
	factoryEntered := make(chan struct{})
	releaseFactory := make(chan struct{})
	oldFactory := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) {
		close(factoryEntered)
		<-releaseFactory
		return mouse, nil
	}
	t.Cleanup(func() { createUinputMouse = oldFactory })

	creationDone := make(chan error, 1)
	go func() {
		creationDone <- b.withMouse(func(uinput.Mouse) error {
			return errors.New("mouse callback must not execute after shutdown")
		})
	}()
	<-factoryEntered
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- b.Close() }()
	deadline := time.Now().Add(3 * time.Second)
	for !b.operations.Closed() {
		if time.Now().After(deadline) {
			close(releaseFactory)
			t.Fatal("shutdown did not start")
		}
		runtime.Gosched()
	}
	close(releaseFactory)
	if err := <-creationDone; !errors.Is(err, errUinputBackendClosed) || !errors.Is(err, cleanupErr) {
		t.Fatalf("lost shutdown or mouse cleanup failure: %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("shutdown should not own factory's unpublished device: %v", err)
	}
	if b.mouse != nil {
		t.Fatal("closed backend published a late-created mouse")
	}
	if events := mouse.eventsSnapshot(); len(events) != 1 || events[0] != "close" {
		t.Fatalf("late mouse was not closed exactly once: %v", events)
	}
}

func TestUinputTypeStopsAfterLiteralWriteFailure(t *testing.T) {
	failure := errors.New("synthetic literal key write failure")
	keyboard := &failingKeyboard{failPress: true, pressErr: failure}
	backend := &UinputBackend{kb: keyboard, charToRune: qwertyRuneMap()}

	err := backend.Type(context.Background(), "a{enter}")
	if !errors.Is(err, failure) {
		t.Fatalf("Type returned %v, want the literal write failure", err)
	}
	if len(keyboard.events) != 0 {
		t.Fatalf("Type sent later key actions after the failed literal write: %v", keyboard.events)
	}
}

func TestUinputTypeStopsAfterLiteralWriteCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keyboard := &uinputCancelOnKeyboardPress{recordingKeyboard: &recordingKeyboard{}, cancel: cancel}
	backend := &UinputBackend{kb: keyboard, charToRune: qwertyRuneMap()}

	err := backend.Type(ctx, "a{enter}")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Type returned %v after cancellation, want context.Canceled", err)
	}
	want := []string{fmt.Sprintf("press:%d", uinput.KeyA)}
	if !slices.Equal(keyboard.events, want) {
		t.Fatalf("Type events after literal cancellation = %v, want %v", keyboard.events, want)
	}
}

// KWin clamps out-of-range virtual touchpad coordinates without reporting an
// injection error. Reject them before issuing any events or clicking an edge.
func TestUinputRejectsOffscreenMoveAndClickWithoutEvents(t *testing.T) {
	touchpad := &recordingTouchPad{}
	withUinputSyntheticConstructors(t,
		func(string) (os.FileInfo, error) { return nil, nil },
		func(string, []byte) (uinput.Keyboard, error) { return &constructorRecordingKeyboard{}, nil },
		func(string, []byte, int32, int32, int32, int32) (uinput.TouchPad, error) { return touchpad, nil },
		func() (map[rune]kernelChar, error) { return qwertyRuneMap(), nil },
	)
	b, err := NewUinputBackend(2867, 1200)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	invalid := []struct {
		name string
		x, y int
	}{
		{"negative X", -1, 600}, {"negative Y", 1400, -1},
		{"first invalid X", 2867, 600}, {"first invalid Y", 1400, 1200},
		{"large negative X", -999, 600}, {"large positive X", 99999, 600},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if err := b.MouseMove(ctx, tc.x, tc.y); err == nil {
				t.Errorf("offscreen move %d,%d was accepted", tc.x, tc.y)
			}
			if err := b.MouseClick(ctx, tc.x, tc.y, 1); err == nil {
				t.Errorf("offscreen click %d,%d was accepted", tc.x, tc.y)
			}
			if len(touchpad.events) != 0 {
				t.Fatalf("offscreen input injected events: %v", touchpad.events)
			}
		})
	}
	for _, tc := range []struct{ x, y int }{{0, 0}, {2866, 0}, {2866, 1199}, {0, 1199}} {
		if err := b.MouseMove(ctx, tc.x, tc.y); err != nil {
			t.Fatalf("valid edge %d,%d rejected: %v", tc.x, tc.y, err)
		}
	}
	if want := []string{"move:0,0", "move:2866,0", "move:2866,1199", "move:0,1199"}; !reflect.DeepEqual(touchpad.events, want) {
		t.Fatalf("valid edge events = %v, want %v", touchpad.events, want)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUinputProbePreservesCharacterDeviceOpenFailure(t *testing.T) {
	info, err := os.Lstat("/dev/tty")
	if err != nil {
		t.Fatalf("stat Linux controlling-terminal device: %v", err)
	}
	if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatalf("/dev/tty is not a character device: %v", info.Mode())
	}

	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err == nil {
		_ = tty.Close()
		t.Skip("the test process has a controlling terminal")
	}
	if !errors.Is(err, syscall.ENXIO) {
		t.Fatalf("open /dev/tty without a controlling terminal: %v, want ENXIO", err)
	}

	got := checkUinputPath("/dev/tty")
	if got.Name != "uinput" || got.Available || !strings.Contains(got.Reason, "/dev/tty open:") || strings.Contains(got.Reason, "permission denied") {
		t.Fatalf("character-device open failure was accepted or misclassified: %+v", got)
	}
}

// A second input operation waiting for an already active operation must not
// inject its events if the caller cancels while waiting for the operation gate.
func TestUinputGateRejectsQueuedOperationAfterCallerCancellation(t *testing.T) {
	b := &UinputBackend{}
	entered := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- b.withOperation(context.Background(), func(context.Context) error { close(entered); <-release; return nil })
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first input operation never acquired gate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	executed := make(chan struct{}, 1)
	go func() {
		queued <- b.withOperation(ctx, func(context.Context) error { executed <- struct{}{}; return nil })
	}()
	// Wait until both operations have entered the lifetime owner, ensuring the
	// second request is blocked at the gate rather than canceled before Begin.
	// Cancellation is also correct when the request has not yet reached Begin.
	cancel()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queued operation remained blocked")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first operation failed: %v", err)
	}
	select {
	case <-executed:
		t.Fatal("canceled operation injected device events")
	default:
	}
	if err := b.withOperation(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("gate not reusable after canceled waiter: %v", err)
	}
}

// Shutdown must cancel queued operations even when an earlier operation owns
// the serialized writer. The already-running operation must drain before Close
// returns, and no blocked command may run after the backend is closed.
func TestUinputGateShutdownCancelsQueuedOperationAndWaitsForActiveWriter(t *testing.T) {
	b := &UinputBackend{}
	entered := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- b.withOperation(context.Background(), func(context.Context) error { close(entered); <-release; return nil })
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("active writer failed to start")
	}
	queued := make(chan error, 1)
	executed := make(chan struct{}, 1)
	go func() {
		queued <- b.withOperation(context.Background(), func(context.Context) error { executed <- struct{}{}; return nil })
	}()
	closed := make(chan error, 1)
	go func() { closed <- b.operations.Close(nil) }()
	select {
	case err := <-closed:
		t.Fatalf("shutdown completed while active writer was still running: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("shutdown failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown failed to drain active writer")
	}
	if err := <-first; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("active canceled writer error=%v", err)
	}
	if err := <-queued; err == nil {
		t.Fatal("queued operation succeeded after shutdown")
	}
	select {
	case <-executed:
		t.Fatal("queued operation ran after shutdown")
	default:
	}
	if err := b.withOperation(context.Background(), func(context.Context) error { return nil }); !errors.Is(err, errUinputBackendClosed) {
		t.Fatalf("closed backend admitted work: %v", err)
	}
}

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

// Constructor failure must not panic during optional output enumeration or
// proceed to create a kernel input device on an uninitialized lister.
func TestUinputOutputDiscoveryRejectsUninitializedLister(t *testing.T) {
	previousOpen, previousCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister, newUinputBackend = previousOpen, previousCreate })
	for _, tc := range []struct {
		name string
		open func(context.Context, env.Runtime) (output.Lister, error)
	}{
		{name: "nil interface", open: func(context.Context, env.Runtime) (output.Lister, error) { return nil, nil }},
		{name: "typed nil", open: func(context.Context, env.Runtime) (output.Lister, error) {
			var v *fakeUinputOutputLister
			return v, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			openUinputOutputLister = tc.open
			newUinputBackend = func(int32, int32) (Inputter, error) {
				t.Fatal("uninitialized output metadata authorized an input device")
				return nil, nil
			}
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("uninitialized output lister caused panic: %v", p)
				}
			}()
			device, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 0, 0)
			if device != nil || err == nil || !strings.Contains(err.Error(), "output") {
				t.Fatalf("uninitialized lister returned backend=%T err=%v", device, err)
			}
		})
	}
}

// An enumeration or teardown error must veto device creation, and release
// any successfully acquired output lister exactly once.
type failingUinputLister struct {
	outputs           []output.Info
	listErr, closeErr error
	closes            int
}

func (l *failingUinputLister) List(context.Context) ([]output.Info, error) {
	return l.outputs, l.listErr
}
func (l *failingUinputLister) Close() error { l.closes++; return l.closeErr }
func TestUinputOutputDiscoveryFailureClosesListerAndRejectsDevice(t *testing.T) {
	previousOpen, previousCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister, newUinputBackend = previousOpen, previousCreate })
	for _, tc := range []struct {
		name              string
		listErr, closeErr error
	}{
		{"list failure", errors.New("list failed"), nil},
		{"close failure", nil, errors.New("close failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &failingUinputLister{outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1920, H: 1080}}}, listErr: tc.listErr, closeErr: tc.closeErr}
			openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) { return l, nil }
			newUinputBackend = func(int32, int32) (Inputter, error) {
				t.Fatal("failed discovery authorized an input device")
				return nil, nil
			}
			device, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 0, 0)
			if device != nil || err == nil || l.closes != 1 {
				t.Fatalf("failed discovery device=%T err=%v closes=%d", device, err, l.closes)
			}
			if tc.listErr != nil && !errors.Is(err, tc.listErr) {
				t.Fatalf("lost enumeration error %v", err)
			}
			if tc.closeErr != nil && !errors.Is(err, tc.closeErr) {
				t.Fatalf("lost teardown error %v", err)
			}
		})
	}
}

// Factories can return a partially initialized device alongside an error.
// Even though the mouse was never published, its file descriptors are owned
// by the caller and must be closed before returning the creation failure.
func TestUinputLazyMouseCreationErrorClosesPartialDevice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*UinputBackend) error
	}{{"scroll", func(b *UinputBackend) error { return b.ScrollDown(context.Background(), 1) }}, {"middle click", func(b *UinputBackend) error { return b.MouseDown(context.Background(), 2) }}} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newUinputActionBackend()
			b.mouse = nil
			partial := &recordingMouse{}
			creationErr := errors.New("synthetic partial mouse creation failure")
			old := createUinputMouse
			createUinputMouse = func(string, []byte) (uinput.Mouse, error) { return partial, creationErr }
			t.Cleanup(func() { createUinputMouse = old })
			if err := tc.invoke(b); !errors.Is(err, creationErr) {
				t.Fatalf("factory failure lost: %v", err)
			}
			if events := partial.eventsSnapshot(); len(events) != 1 || events[0] != "close" {
				t.Fatalf("partially created mouse not closed exactly once: %v", events)
			}
			if b.mouse != nil {
				t.Fatal("failed mouse creation retained unauthorized device")
			}
		})
	}
}

func TestUinputLazyMouseCreationPreservesRollbackFailure(t *testing.T) {
	b, _, _ := newUinputActionBackend()
	b.mouse = nil
	cleanupErr := errors.New("partial mouse close failed")
	partial := &recordingMouse{closeErr: cleanupErr}
	creationErr := errors.New("partial mouse create failed")
	old := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) { return partial, creationErr }
	t.Cleanup(func() { createUinputMouse = old })
	err := b.ScrollUp(context.Background(), 1)
	if !errors.Is(err, creationErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("creation and rollback errors not retained: %v", err)
	}
	if events := partial.eventsSnapshot(); len(events) != 1 || events[0] != "close" {
		t.Fatalf("partial mouse not closed: %v", events)
	}
}

// A virtual device may report an error after a partial press write. The
// physical state is then uncertain, so a click must attempt a compensating
// release rather than leaving the button potentially held.
type uncertainPressTouchpad struct {
	recordingTouchPad
	pressErr   error
	releaseErr error
}

func (p *uncertainPressTouchpad) LeftPress() error {
	p.record("left-press")
	return p.pressErr
}
func (p *uncertainPressTouchpad) LeftRelease() error {
	p.record("left-release")
	return p.releaseErr
}

func TestUinputClickAttemptsReleaseAfterUncertainPressFailure(t *testing.T) {
	for _, tc := range []struct {
		name         string
		releaseFails bool
	}{
		{name: "release succeeds"},
		{name: "release fails", releaseFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pressFailure := errors.New("partial press write failed")
			releaseFailure := errors.New("compensating release failed")
			tp := &uncertainPressTouchpad{pressErr: pressFailure}
			if tc.releaseFails {
				tp.releaseErr = releaseFailure
			}
			b := &UinputBackend{touchpad: tp}
			err := b.MouseClick(context.Background(), 12, 34, 1)
			if !errors.Is(err, pressFailure) {
				t.Fatalf("partial press error lost: %v", err)
			}
			if errors.Is(err, releaseFailure) != tc.releaseFails {
				t.Fatalf("cleanup failure not preserved: %v", err)
			}
			if want := []string{"move:12,34", "left-press", "left-release"}; !slices.Equal(tp.events, want) {
				t.Fatalf("partial press cleanup events=%v, want=%v", tp.events, want)
			}
		})
	}
}

// A failed device setup or an injected partially built backend must never
// dereference a missing uinput device. Each operation fails closed rather
// than crashing the automation process.
func TestUinputPartiallyAcquiredDevicesFailClosedWithoutPanic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend func() *UinputBackend
		execute func(*UinputBackend) error
	}{
		{"key down missing keyboard", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.KeyDown(context.Background(), "a") }},
		{"key up missing keyboard", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.KeyUp(context.Background(), "a") }},
		{"literal type missing keyboard", func() *UinputBackend { return &UinputBackend{charToRune: qwertyRuneMap()} }, func(b *UinputBackend) error { return b.TypeLiteral(context.Background(), "abc") }},
		{"syntax type missing keyboard", func() *UinputBackend { return &UinputBackend{charToRune: qwertyRuneMap()} }, func(b *UinputBackend) error { return b.Type(context.Background(), "abc") }},
		{"move missing touchpad", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.MouseMove(context.Background(), 12, 23) }},
		{"click missing touchpad", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.MouseClick(context.Background(), 12, 23, 1) }},
		{"down missing touchpad", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.MouseDown(context.Background(), 1) }},
		{"up missing touchpad", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.MouseUp(context.Background(), 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.backend()
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("missing device caused panic: %v", recovered)
				}
			}()
			if err := tc.execute(b); !errors.Is(err, ErrNotSupported) {
				t.Errorf("missing device result=%v, want typed not-supported", err)
			}
		})
	}
}

func TestUinputPointerTargetFailClosedBoundaryMatrix(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	closed := &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		backend  *UinputBackend
		ctx      context.Context
		x, y     int
		want     string
		canceled bool
	}{
		{name: "nil backend", want: "unavailable"},
		{name: "closed backend", backend: closed, want: "closed"},
		{name: "unknown bounds", backend: &UinputBackend{}, want: "bounds unavailable"},
		{name: "width one", backend: &UinputBackend{logicalWidth: 1, logicalHeight: 1080}, want: "bounds unavailable"},
		{name: "positive x overflow", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, x: math.MaxInt, want: "int32 range"},
		{name: "negative y overflow", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, y: math.MinInt, want: "int32 range"},
		{name: "negative x", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, x: -1, want: "outside logical desktop bounds"},
		{name: "negative y", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, y: -1, want: "outside logical desktop bounds"},
		{name: "right boundary", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, x: 1920, want: "outside logical desktop bounds"},
		{name: "bottom boundary", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, y: 1080, want: "outside logical desktop bounds"},
		{name: "last valid pixel", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, x: 1919, y: 1079},
		{name: "origin valid", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}},
		{name: "cancellation precedes nil backend", ctx: canceled, canceled: true},
		{name: "cancellation precedes invalid bounds", backend: &UinputBackend{}, ctx: canceled, canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			err := tc.backend.ValidatePointerTarget(ctx, tc.x, tc.y)
			if tc.canceled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v want cancellation", err)
				}
				return
			}
			if tc.want == "" {
				if err != nil {
					t.Fatalf("valid point rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}

// Simulate cancellation immediately after a valid explicit logical geometry
// is checked, without relying on scheduler timing or a real kernel device.
type cancelAtSecondUinputCheck struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelAtSecondUinputCheck) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestUinputCancellationAfterGeometryValidationDoesNotOpenDevice(t *testing.T) {
	original := newUinputBackend
	t.Cleanup(func() { newUinputBackend = original })
	created := 0
	newUinputBackend = func(int32, int32) (Inputter, error) {
		created++
		return noopInputter{}, nil
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAtSecondUinputCheck{Context: base, cancel: cancel}
	backend, err := newResolvedUinputBackend(ctx, env.Runtime{}, 1920, 1080)
	if backend != nil || !errors.Is(err, context.Canceled) || created != 0 || ctx.checks != 2 {
		t.Fatalf("post-validation cancellation admitted kernel device: backend=%T err=%v created=%d checks=%d", backend, err, created, ctx.checks)
	}
}

// An already canceled uinput operation must not reject an invalid payload
// first. A canceled caller is not permitted to inject mouse events or
// distinguish individual payload validation results.
func TestUinputCanceledButtonAndScrollRequestsRejectBeforePayloadValidation(t *testing.T) {
	b := &UinputBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name   string
		invoke func(context.Context) error
	}{
		{"click", func(ctx context.Context) error { return b.MouseClick(ctx, 10, 10, 99) }},
		{"down", func(ctx context.Context) error { return b.MouseDown(ctx, 99) }},
		{"up", func(ctx context.Context) error { return b.MouseUp(ctx, 99) }},
		{"scroll up", func(ctx context.Context) error { return b.ScrollUp(ctx, -1) }},
		{"scroll down", func(ctx context.Context) error { return b.ScrollDown(ctx, -1) }},
		{"scroll left", func(ctx context.Context) error { return b.ScrollLeft(ctx, -1) }},
		{"scroll right", func(ctx context.Context) error { return b.ScrollRight(ctx, -1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.invoke(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled call returned %v instead of canceled", err)
			}
		})
	}
}

// A malformed or unmappable tail must never leave a prefix typed into a
// focused application. Syntactic parsing alone cannot establish that every
// token can be translated to the current kernel keyboard layout.
func TestUinputRejectsUnmappableKeySequencesBeforeSendingAnyInput(t *testing.T) {
	for _, tc := range []struct {
		name, sequence string
		literal        bool
	}{
		{"unsupported key after valid text", "some text{not-a-real-key}", false},
		{"unsupported character after valid text", "abc€", false},
		{"unsupported key after valid combination", "{ctrl+a}{not-a-real-key}", false},
		{"unsupported literal after valid text", "valid€", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, kb := newTestBackend(t)
			var err error
			if tc.literal {
				err = b.TypeLiteral(context.Background(), tc.sequence)
			} else {
				err = b.Type(context.Background(), tc.sequence)
			}
			if err == nil {
				t.Fatal("unmappable sequence accepted")
			}
			if len(kb.events) != 0 {
				t.Fatalf("unmappable sequence injected partial keyboard events: %v", kb.events)
			}
		})
	}
}

func TestUinputRejectsMalformedSyntaxWithoutInjectingInput(t *testing.T) {
	for _, sequence := range []string{"before{ctrl+}", "before{alt+ctrl}", "before{unclosed"} {
		b, kb := newTestBackend(t)
		if err := b.Type(context.Background(), sequence); err == nil {
			t.Fatalf("malformed syntax accepted: %q", sequence)
		}
		if len(kb.events) != 0 {
			t.Fatalf("malformed syntax typed before failing %q: %v", sequence, kb.events)
		}
	}
}

func TestUinputPreflightStillAcceptsValidMultistepCommands(t *testing.T) {
	b, kb := newTestBackend(t)
	sequence := "Ab{ctrl+s}cd"
	if err := b.Type(context.Background(), sequence); err != nil {
		t.Fatalf("valid sequence rejected: %v", err)
	}
	if len(kb.events) == 0 || !strings.Contains(strings.Join(kb.events, ";"), "press:") {
		t.Fatalf("valid sequence never reached keyboard: %v", kb.events)
	}
}

// Interface values with typed nil pointers are non-nil, but represent no
// acquired mouse. Lazy creation must not publish such an invalid device.
func TestUinputLazyMouseRejectsTypedNilFactorySuccess(t *testing.T) {
	b, _, _ := newUinputActionBackend()
	b.mouse = nil
	old := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) { var absent *recordingMouse; return absent, nil }
	t.Cleanup(func() { createUinputMouse = old })
	err := b.ensureMouseLocked()
	if err == nil || !strings.Contains(err.Error(), "nil device") {
		t.Fatalf("typed-nil factory success accepted: %v", err)
	}
	if !util.IsNil(b.mouse) {
		t.Fatal("invalid mouse published")
	}
}

// A release for a modifier not owned by this caller is not safe to inject:
// it can clear a shortcut being held by another desktop input client.
func TestUinputRejectsUnheldModifierReleaseWithoutEmittingEvents(t *testing.T) {
	for _, name := range []string{"ctrl", "shift", "alt", "super"} {
		t.Run(name, func(t *testing.T) {
			b, kb := newTestBackend(t)
			err := b.KeyUp(context.Background(), name)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld %s KeyUp result=%v", name, err)
			}
			if len(kb.events) > 0 {
				t.Fatalf("unheld modifier injected key-up: %v", kb.events)
			}
		})
	}
}

// A malformed tail must invalidate the complete command, not merely fail
// after it has already typed a prefix into the active application.
func TestUinputRejectsUnheldModifierTailBeforeTypingPrefix(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"literal prefix", "hello{ctrl up}"},
		{"chord prefix", "{ctrl+a}{shift up}"},
		{"double release", "{ctrl down}{ctrl up}{ctrl up}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, kb := newTestBackend(t)
			err := b.Type(context.Background(), tc.command)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld sequence %q accepted: %v", tc.command, err)
			}
			if len(kb.events) != 0 {
				t.Fatalf("failed sequence %q typed partially: %v", tc.command, kb.events)
			}
		})
	}
}

func TestUinputPreflightPreservesHeldModifierAndValidDownUpSequence(t *testing.T) {
	b, kb := newTestBackend(t)
	if err := b.KeyDown(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	if err := b.Type(context.Background(), "hello{ctrl+s}{ctrl up}"); err != nil {
		t.Fatalf("valid held sequence rejected: %v", err)
	}
	if b.modifierHeld(29) {
		t.Fatal("ctrl remained held after explicit release")
	}
	if len(kb.events) == 0 {
		t.Fatal("valid sequence emitted no keyboard events")
	}
}

// Physical key identifiers are ASCII protocol names, not locale-sensitive
// Unicode text. Case folding must not turn a non-ASCII symbol into a different
// authorized physical key.
func TestUinputRejectsUnicodeAliasesOfPhysicalKeys(t *testing.T) {
	backend := &UinputBackend{}
	for _, tc := range []struct {
		key  string
		want int
	}{
		{"i", uinput.KeyI},
		{"I", uinput.KeyI},
		{"k", uinput.KeyK},
		{"K", uinput.KeyK},
	} {
		got, err := backend.resolveKey(tc.key)
		if err != nil || got != tc.want {
			t.Fatalf("ASCII physical key %q resolved to %d err=%v, want %d", tc.key, got, err, tc.want)
		}
	}
	for _, key := range []string{"İ", "K", "ſ", "ı"} {
		got, err := backend.resolveKey(key)
		if err == nil || got != 0 || !strings.Contains(err.Error(), "unknown key") {
			t.Errorf("Unicode alias %q resolved to physical key %d err=%v", key, got, err)
		}
	}
}

type syntheticFaultKeyboard struct {
	*recordingKeyboard
	failAt  int
	calls   int
	failure error
}

func (k *syntheticFaultKeyboard) fail() error {
	k.calls++
	if k.calls == k.failAt {
		return k.failure
	}
	return nil
}
func (k *syntheticFaultKeyboard) KeyDown(code int) error {
	k.events = append(k.events, fmt.Sprintf("down:%d", code))
	return k.fail()
}
func (k *syntheticFaultKeyboard) KeyUp(code int) error {
	k.events = append(k.events, fmt.Sprintf("up:%d", code))
	return k.fail()
}
func (k *syntheticFaultKeyboard) KeyPress(code int) error {
	k.events = append(k.events, fmt.Sprintf("press:%d", code))
	return k.fail()
}

var _ uinput.Keyboard = (*syntheticFaultKeyboard)(nil)

func TestUinputTemporaryModifierFailureRollsBackRealWireSequence(t *testing.T) {
	failure := errors.New("synthetic evdev device failure")
	ctrl, a := uinput.KeyLeftctrl, uinput.KeyA
	wantSuccess := []string{fmt.Sprintf("down:%d", ctrl), fmt.Sprintf("press:%d", a), fmt.Sprintf("up:%d", ctrl)}
	for _, tc := range []struct {
		failAt int
		want   []string
	}{
		{1, wantSuccess[:1]},
		{2, wantSuccess},
		{3, wantSuccess},
		{0, wantSuccess},
	} {
		t.Run(fmt.Sprintf("fail_at_%d", tc.failAt), func(t *testing.T) {
			kb := &syntheticFaultKeyboard{recordingKeyboard: &recordingKeyboard{}, failAt: tc.failAt, failure: failure}
			b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
			err := b.Type(context.Background(), "{ctrl+a}")
			if (tc.failAt != 0) != errors.Is(err, failure) {
				t.Fatalf("evdev failure at %d reported %v", tc.failAt, err)
			}
			if !slices.Equal(kb.events, tc.want) {
				t.Fatalf("injected failure at %d events=%v, want %v", tc.failAt, kb.events, tc.want)
			}
			if b.modifierHeld(ctrl) {
				t.Fatal("temporary modifier leaked into caller-owned hold state")
			}
		})
	}
}

func TestUinputLiteralShiftFailurePreservesErrorAndAttemptsRelease(t *testing.T) {
	failure := errors.New("synthetic evdev device failure")
	for _, tc := range []struct {
		failAt int
		want   []string
	}{
		{1, []string{fmt.Sprintf("down:%d", uinput.KeyLeftshift)}},
		{2, []string{fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("press:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyLeftshift)}},
		{3, []string{fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("press:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyLeftshift)}},
		{0, []string{fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("press:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyLeftshift)}},
	} {
		t.Run(fmt.Sprintf("fail_at_%d", tc.failAt), func(t *testing.T) {
			kb := &syntheticFaultKeyboard{recordingKeyboard: &recordingKeyboard{}, failAt: tc.failAt, failure: failure}
			b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
			err := b.TypeLiteral(context.Background(), "A")
			if (tc.failAt != 0) != errors.Is(err, failure) {
				t.Fatalf("evdev failure at %d reported %v", tc.failAt, err)
			}
			if !slices.Equal(kb.events, tc.want) {
				t.Fatalf("injected failure at %d events=%v want %v", tc.failAt, kb.events, tc.want)
			}
			if b.modifierHeld(uinput.KeyLeftshift) {
				t.Fatal("temporary shift leaked into caller-held state")
			}
		})
	}
}

type cancelAfterModifierDownKeyboard struct {
	*recordingKeyboard
	cancel         context.CancelFunc
	cancelCode     int
	releaseFailure error
}

func (k *cancelAfterModifierDownKeyboard) KeyDown(code int) error {
	err := k.recordingKeyboard.KeyDown(code)
	if code == k.cancelCode {
		k.cancel()
	}
	return err
}
func (k *cancelAfterModifierDownKeyboard) KeyUp(code int) error {
	err := k.recordingKeyboard.KeyUp(code)
	if k.releaseFailure != nil && code == uinput.KeyLeftctrl {
		return errors.Join(err, k.releaseFailure)
	}
	return err
}

var _ uinput.Keyboard = (*cancelAfterModifierDownKeyboard)(nil)

// In a multi-modifier chord, caller cancellation can occur immediately after
// one or more modifier-down writes. Any temporary hold already sent must be
// released in reverse order; the target key must never be injected.
func TestUinputCanceledPartialChordReleasesTemporaryModifiers(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cancelCode  int
		want        []string
		failRelease bool
	}{
		{"cancel after first modifier", uinput.KeyLeftctrl, []string{
			fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("down:%d", uinput.KeyLeftctrl), fmt.Sprintf("up:%d", uinput.KeyLeftctrl), fmt.Sprintf("up:%d", uinput.KeyLeftshift),
		}, false},
		{"cancel after final modifier", uinput.KeyLeftalt, []string{
			fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("down:%d", uinput.KeyLeftctrl), fmt.Sprintf("down:%d", uinput.KeyLeftalt), fmt.Sprintf("up:%d", uinput.KeyLeftalt), fmt.Sprintf("up:%d", uinput.KeyLeftctrl), fmt.Sprintf("up:%d", uinput.KeyLeftshift),
		}, false},
		{"rollback release also fails", uinput.KeyLeftctrl, []string{
			fmt.Sprintf("down:%d", uinput.KeyLeftshift), fmt.Sprintf("down:%d", uinput.KeyLeftctrl), fmt.Sprintf("up:%d", uinput.KeyLeftctrl), fmt.Sprintf("up:%d", uinput.KeyLeftshift),
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			releaseFailure := errors.New("synthetic modifier release error")
			kb := &cancelAfterModifierDownKeyboard{recordingKeyboard: &recordingKeyboard{}, cancel: cancel, cancelCode: tc.cancelCode}
			if tc.failRelease {
				kb.releaseFailure = releaseFailure
			}
			backend := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
			err := backend.Type(ctx, "{shift+ctrl+alt+a}")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled chord returned %v", err)
			}
			if errors.Is(err, releaseFailure) != tc.failRelease {
				t.Fatalf("cleanup error not preserved correctly: %v", err)
			}
			if !slices.Equal(kb.events, tc.want) {
				t.Fatalf("incomplete chord events=%v, want %v", kb.events, tc.want)
			}
			for _, code := range []int{uinput.KeyLeftshift, uinput.KeyLeftctrl, uinput.KeyLeftalt} {
				if backend.modifierHeld(code) {
					t.Fatalf("temporary modifier %d became caller-owned", code)
				}
			}
		})
	}
}

// TypeLiteral verifies each rune's lifetime after the previous keyboard write:
// a device succeeding on character one cannot authorize character two after
// the caller cancels, even when the first output was valid.
func TestUinputLiteralCancellationCannotTypeFollowingCharacters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kb := &uinputCancelOnKeyboardPress{recordingKeyboard: &recordingKeyboard{}, cancel: cancel}
	backend := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	if err := backend.TypeLiteral(ctx, "ab"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled literal reported %v", err)
	}
	want := []string{fmt.Sprintf("press:%d", uinput.KeyA)}
	if !slices.Equal(kb.events, want) {
		t.Fatalf("typed after revocation: %v", kb.events)
	}
}

type recordingKeyboard struct {
	events   []string
	closeErr error
}

func (k *recordingKeyboard) KeyPress(key int) error {
	k.events = append(k.events, fmt.Sprintf("press:%d", key))
	return nil
}

func (k *recordingKeyboard) KeyDown(key int) error {
	k.events = append(k.events, fmt.Sprintf("down:%d", key))
	return nil
}

func (k *recordingKeyboard) KeyUp(key int) error {
	k.events = append(k.events, fmt.Sprintf("up:%d", key))
	return nil
}

func (k *recordingKeyboard) FetchSyspath() (string, error) { return "", nil }

func (k *recordingKeyboard) Close() error { return k.closeErr }

var _ uinput.Keyboard = (*recordingKeyboard)(nil)

type recordingTouchPad struct {
	events   []string
	closeErr error
}

func (t *recordingTouchPad) record(s string) { t.events = append(t.events, s) }
func (t *recordingTouchPad) MoveTo(x int32, y int32) error {
	t.record(fmt.Sprintf("move:%d,%d", x, y))
	return nil
}
func (t *recordingTouchPad) LeftClick() error  { t.record("left-click"); return nil }
func (t *recordingTouchPad) RightClick() error { t.record("right-click"); return nil }
func (t *recordingTouchPad) LeftPress() error  { t.record("left-press"); return nil }
func (t *recordingTouchPad) LeftRelease() error {
	t.record("left-release")
	return nil
}
func (t *recordingTouchPad) RightPress() error { t.record("right-press"); return nil }
func (t *recordingTouchPad) RightRelease() error {
	t.record("right-release")
	return nil
}
func (t *recordingTouchPad) TouchDown() error              { t.record("touch-down"); return nil }
func (t *recordingTouchPad) TouchUp() error                { t.record("touch-up"); return nil }
func (t *recordingTouchPad) FetchSyspath() (string, error) { return "", nil }
func (t *recordingTouchPad) Close() error                  { t.record("close"); return t.closeErr }

var _ uinput.TouchPad = (*recordingTouchPad)(nil)

type cancelingTouchPad struct {
	recordingTouchPad
	cancel context.CancelFunc
}

func (t *cancelingTouchPad) LeftPress() error {
	t.record("left-press")
	t.cancel()
	return nil
}

var _ uinput.TouchPad = (*cancelingTouchPad)(nil)

type recordingMouse struct {
	mu       sync.Mutex
	events   []string
	closeErr error
}

func (m *recordingMouse) record(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, s)
}
func (m *recordingMouse) eventsSnapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.events...)
}
func (m *recordingMouse) MoveLeft(pixel int32) error {
	m.record(fmt.Sprintf("move-left:%d", pixel))
	return nil
}
func (m *recordingMouse) MoveRight(pixel int32) error {
	m.record(fmt.Sprintf("move-right:%d", pixel))
	return nil
}
func (m *recordingMouse) MoveUp(pixel int32) error {
	m.record(fmt.Sprintf("move-up:%d", pixel))
	return nil
}
func (m *recordingMouse) MoveDown(pixel int32) error {
	m.record(fmt.Sprintf("move-down:%d", pixel))
	return nil
}
func (m *recordingMouse) Move(x, y int32) error {
	m.record(fmt.Sprintf("move:%d,%d", x, y))
	return nil
}
func (m *recordingMouse) LeftClick() error     { m.record("left-click"); return nil }
func (m *recordingMouse) RightClick() error    { m.record("right-click"); return nil }
func (m *recordingMouse) MiddleClick() error   { m.record("middle-click"); return nil }
func (m *recordingMouse) LeftPress() error     { m.record("left-press"); return nil }
func (m *recordingMouse) LeftRelease() error   { m.record("left-release"); return nil }
func (m *recordingMouse) RightPress() error    { m.record("right-press"); return nil }
func (m *recordingMouse) RightRelease() error  { m.record("right-release"); return nil }
func (m *recordingMouse) MiddlePress() error   { m.record("middle-press"); return nil }
func (m *recordingMouse) MiddleRelease() error { m.record("middle-release"); return nil }
func (m *recordingMouse) Wheel(horizontal bool, delta int32) error {
	m.record(fmt.Sprintf("wheel:%t:%d", horizontal, delta))
	return nil
}
func (m *recordingMouse) FetchSyspath() (string, error) { return "", nil }
func (m *recordingMouse) FetchSysPath() (string, error) { return "", nil }
func (m *recordingMouse) Close() error                  { m.record("close"); return m.closeErr }

var _ uinput.Mouse = (*recordingMouse)(nil)

type blockingMouse struct {
	recordingMouse
	started     chan struct{}
	release     chan struct{}
	startedOnce func()
}

func (m *blockingMouse) Wheel(horizontal bool, delta int32) error {
	if err := m.recordingMouse.Wheel(horizontal, delta); err != nil {
		return err
	}
	m.startedOnce()
	<-m.release
	return nil
}

var _ uinput.Mouse = (*blockingMouse)(nil)

// newTestBackend creates a UinputBackend with a recording keyboard and the
// QWERTY fallback rune map for tests that don't depend on kernel keymap probing.
func newTestBackend(t *testing.T) (*UinputBackend, *recordingKeyboard) {
	t.Helper()
	kb := &recordingKeyboard{}
	return &UinputBackend{
		kb:         kb,
		charToRune: qwertyRuneMap(),
	}, kb
}

func newUinputActionBackend() (*UinputBackend, *recordingTouchPad, *recordingMouse) {
	kb := &recordingKeyboard{}
	tp := &recordingTouchPad{}
	mouse := &recordingMouse{}
	return &UinputBackend{
		kb:         kb,
		touchpad:   tp,
		mouse:      mouse,
		charToRune: qwertyRuneMap(),
	}, tp, mouse
}

func TestUinputKeyDownAndUp(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.KeyDown(context.Background(), "a"); err != nil {
		t.Fatalf("KeyDown: %v", err)
	}
	if err := b.KeyUp(context.Background(), "a"); err != nil {
		t.Fatalf("KeyUp: %v", err)
	}

	want := []string{fmt.Sprintf("down:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyA)}
	if len(kb.events) != len(want) {
		t.Fatalf("events = %v, want %v", kb.events, want)
	}
	for i, exp := range want {
		if kb.events[i] != exp {
			t.Fatalf("event %d = %q, want %q", i, kb.events[i], exp)
		}
	}
}

func TestUinputTypeContextExplicitKeyUpReleasesHeldKey(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.typeContext(context.Background(), "{ctrl down}{ctrl up}"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []string{
		fmt.Sprintf("down:%d", uinput.KeyLeftctrl),
		fmt.Sprintf("up:%d", uinput.KeyLeftctrl),
	}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputTypeContextUppercaseUsesShift(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.Type(context.Background(), "A"); err != nil {
		t.Fatalf("Type: %v", err)
	}

	want := []string{
		fmt.Sprintf("down:%d", uinput.KeyLeftshift),
		fmt.Sprintf("press:%d", uinput.KeyA),
		fmt.Sprintf("up:%d", uinput.KeyLeftshift),
	}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputTypeContextLowercaseDoesNotUseShift(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.typeContext(context.Background(), "a"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []string{fmt.Sprintf("press:%d", uinput.KeyA)}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputTypeContextSymbolUsesShift(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.typeContext(context.Background(), "!"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []string{
		fmt.Sprintf("down:%d", uinput.KeyLeftshift),
		fmt.Sprintf("press:%d", uinput.Key1),
		fmt.Sprintf("up:%d", uinput.KeyLeftshift),
	}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputTypeContextMixedText(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.typeContext(context.Background(), "Hi!"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []string{
		// H: shift + h
		fmt.Sprintf("down:%d", uinput.KeyLeftshift),
		fmt.Sprintf("press:%d", uinput.KeyH),
		fmt.Sprintf("up:%d", uinput.KeyLeftshift),
		// i: no shift
		fmt.Sprintf("press:%d", uinput.KeyI),
		// !: shift + 1
		fmt.Sprintf("down:%d", uinput.KeyLeftshift),
		fmt.Sprintf("press:%d", uinput.Key1),
		fmt.Sprintf("up:%d", uinput.KeyLeftshift),
	}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputTypeContextUnsupportedChar(t *testing.T) {
	b, kb := newTestBackend(t)

	// Euro sign is not in the QWERTY fallback map
	err := b.typeContext(context.Background(), "€")
	if err == nil {
		t.Fatal("expected error for unsupported character, got nil")
	}
	if kb.events != nil {
		t.Fatalf("expected no events for unsupported character, got %v", kb.events)
	}
}

func TestUinputTypeContextWithKeyCombo(t *testing.T) {
	b, kb := newTestBackend(t)

	// {ctrl+a} should use resolveKey path, not charToRune
	if err := b.typeContext(context.Background(), "{ctrl+a}"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []string{
		// ctrl down
		fmt.Sprintf("down:%d", uinput.KeyLeftctrl),
		// a press
		fmt.Sprintf("press:%d", uinput.KeyA),
		// ctrl up
		fmt.Sprintf("up:%d", uinput.KeyLeftctrl),
	}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputMouseActionsAndScroll(t *testing.T) { //nolint:gocyclo
	b, tp, mouse := newUinputActionBackend()

	t.Run("MouseMove", func(t *testing.T) {
		tp.events = nil
		if err := b.MouseMove(context.Background(), 11, 22); err != nil {
			t.Fatalf("MouseMove: %v", err)
		}
		want := []string{"move:11,22"}
		if len(tp.events) != len(want) {
			t.Fatalf("events = %v, want %v", tp.events, want)
		}
		for i, exp := range want {
			if tp.events[i] != exp {
				t.Fatalf("event %d = %q, want %q", i, tp.events[i], exp)
			}
		}
	})

	t.Run("MouseClick", func(t *testing.T) {
		tp.events = nil
		if err := b.MouseClick(context.Background(), 3, 4, 1); err != nil {
			t.Fatalf("MouseClick: %v", err)
		}
		want := []string{"move:3,4", "left-press", "left-release"}
		if len(tp.events) != len(want) {
			t.Fatalf("events = %v, want %v", tp.events, want)
		}
		for i, exp := range want {
			if tp.events[i] != exp {
				t.Fatalf("event %d = %q, want %q", i, tp.events[i], exp)
			}
		}
	})

	t.Run("MouseButtons", func(t *testing.T) {
		tp.events = nil
		mouse.events = nil

		if err := b.MouseDown(context.Background(), 1); err != nil {
			t.Fatalf("MouseDown left: %v", err)
		}
		if err := b.MouseUp(context.Background(), 3); err != nil {
			t.Fatalf("MouseUp right: %v", err)
		}
		if err := b.MouseDown(context.Background(), 2); err != nil {
			t.Fatalf("MouseDown middle: %v", err)
		}
		if err := b.MouseUp(context.Background(), 2); err != nil {
			t.Fatalf("MouseUp middle: %v", err)
		}

		if got := tp.events; len(got) != 2 || got[0] != "left-press" || got[1] != "right-release" {
			t.Fatalf("touchpad events = %v, want [left-press right-release]", got)
		}
		if got := mouse.events; len(got) != 2 || got[0] != "middle-press" || got[1] != "middle-release" {
			t.Fatalf("mouse events = %v, want [middle-press middle-release]", got)
		}
	})

	t.Run("Scroll", func(t *testing.T) {
		mouse.events = nil
		if err := b.ScrollUp(context.Background(), 2); err != nil {
			t.Fatalf("ScrollUp: %v", err)
		}
		if err := b.ScrollRight(context.Background(), 3); err != nil {
			t.Fatalf("ScrollRight: %v", err)
		}
		if len(mouse.events) != 2 {
			t.Fatalf("mouse events = %v, want 2 entries", mouse.events)
		}
		if mouse.events[0] != "wheel:false:-2" {
			t.Fatalf("ScrollUp event = %q, want wheel:false:-2", mouse.events[0])
		}
		if mouse.events[1] != "wheel:true:3" {
			t.Fatalf("ScrollRight event = %q, want wheel:true:3", mouse.events[1])
		}
	})
}

func TestUinputMouseMoveRejectsCoordinatesThatWouldTruncate(t *testing.T) {
	b, tp, _ := newUinputActionBackend()
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{name: "x above", x: int(int64(1 << 31)), y: 0},
		{name: "x below", x: int(-int64(1<<31) - 1), y: 0},
		{name: "y above", x: 0, y: int(int64(1 << 31))},
		{name: "y below", x: 0, y: int(-int64(1<<31) - 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := b.MouseMove(context.Background(), tc.x, tc.y); err == nil {
				t.Fatal("MouseMove accepted a coordinate outside the int32 range")
			}
		})
	}
	if len(tp.events) != 0 {
		t.Fatalf("touchpad events = %v, want no event after validation failures", tp.events)
	}
}

func TestUinputMouseClickReleasesAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tp := &cancelingTouchPad{cancel: cancel}
	b := &UinputBackend{touchpad: tp}

	err := b.MouseClick(ctx, 3, 4, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MouseClick error = %v, want context.Canceled", err)
	}
	want := []string{"move:3,4", "left-press", "left-release"}
	if fmt.Sprint(tp.events) != fmt.Sprint(want) {
		t.Fatalf("touchpad events = %v, want %v", tp.events, want)
	}
}

func TestUinputMouseClickHoldsBeforeRelease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	tp := &recordingTouchPad{}
	b := &UinputBackend{touchpad: tp}

	err := b.MouseClick(ctx, 3, 4, 1)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("MouseClick error = %v, want context.DeadlineExceeded", err)
	}
	want := []string{"move:3,4", "left-press", "left-release"}
	if fmt.Sprint(tp.events) != fmt.Sprint(want) {
		t.Fatalf("touchpad events = %v, want %v", tp.events, want)
	}
}

func TestUinputPointerLocationUnsupported(t *testing.T) {
	b, _ := newTestBackend(t)
	if _, _, err := b.PointerLocation(context.Background()); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("PointerLocation error = %v, want ErrNotSupported", err)
	}
}

func TestUinputCloseClosesAllDevices(t *testing.T) {
	kb := &recordingKeyboard{closeErr: errors.New("keyboard close failed")}
	tp := &recordingTouchPad{closeErr: errors.New("touchpad close failed")}
	mouse := &recordingMouse{closeErr: errors.New("mouse close failed")}
	b := &UinputBackend{
		kb:         kb,
		touchpad:   tp,
		mouse:      mouse,
		charToRune: qwertyRuneMap(),
	}

	err := b.Close()
	if !errors.Is(err, kb.closeErr) || !errors.Is(err, tp.closeErr) || !errors.Is(err, mouse.closeErr) {
		t.Fatalf("Close error = %v, want joined close errors", err)
	}
	if len(kb.events) != 0 || len(tp.events) != 1 || len(mouse.events) != 1 {
		t.Fatalf("close events = kb:%v tp:%v mouse:%v", kb.events, tp.events, mouse.events)
	}
	if tp.events[0] != "close" || mouse.events[0] != "close" {
		t.Fatalf("close events = tp:%v mouse:%v, want close entries", tp.events, mouse.events)
	}
	if repeatedErr := b.Close(); !errors.Is(repeatedErr, kb.closeErr) ||
		!errors.Is(repeatedErr, tp.closeErr) || !errors.Is(repeatedErr, mouse.closeErr) {
		t.Fatalf("repeated Close error = %v, want the original joined close errors", repeatedErr)
	}
}

func TestUinputConcurrentLazyMouseCreation(t *testing.T) {
	kb := &recordingKeyboard{}
	tp := &recordingTouchPad{}
	mouse := &recordingMouse{}
	b := &UinputBackend{kb: kb, touchpad: tp, charToRune: qwertyRuneMap()}

	var factoryMu sync.Mutex
	var factoryCalls int
	factoryStarted := make(chan struct{})
	factoryRelease := make(chan struct{})
	factoryStartedOnce := sync.OnceFunc(func() { close(factoryStarted) })
	oldFactory := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) {
		factoryMu.Lock()
		factoryCalls++
		factoryMu.Unlock()
		factoryStartedOnce()
		<-factoryRelease
		return mouse, nil
	}
	t.Cleanup(func() { createUinputMouse = oldFactory })

	const workers = 8
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			if i%2 == 0 {
				results <- b.ScrollUp(context.Background(), 1)
				return
			}
			results <- b.ScrollRight(context.Background(), 1)
		}(i)
	}

	select {
	case <-factoryStarted:
	case <-time.After(time.Second):
		t.Fatal("lazy mouse factory did not start")
	}
	factoryMu.Lock()
	if factoryCalls != 1 {
		factoryMu.Unlock()
		t.Fatalf("mouse factory calls while creation blocked = %d, want 1", factoryCalls)
	}
	factoryMu.Unlock()
	close(factoryRelease)

	for i := 0; i < workers; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("concurrent scroll %d: %v", i, err)
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent scroll did not finish")
		}
	}

	factoryMu.Lock()
	if factoryCalls != 1 {
		t.Fatalf("mouse factory calls = %d, want 1", factoryCalls)
	}
	factoryMu.Unlock()
	events := mouse.eventsSnapshot()
	if got := len(events); got != workers {
		t.Fatalf("mouse events = %v, want %d wheel events", events, workers)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestUinputCloseDuringLazyMouseCreation(t *testing.T) {
	kb := &recordingKeyboard{}
	tp := &recordingTouchPad{}
	mouse := &recordingMouse{}
	b := &UinputBackend{kb: kb, touchpad: tp, charToRune: qwertyRuneMap()}

	factoryStarted := make(chan struct{})
	factoryRelease := make(chan struct{})
	factoryStartedOnce := sync.OnceFunc(func() { close(factoryStarted) })
	oldFactory := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) {
		factoryStartedOnce()
		<-factoryRelease
		return mouse, nil
	}
	t.Cleanup(func() { createUinputMouse = oldFactory })

	scrollDone := make(chan error, 1)
	go func() { scrollDone <- b.ScrollRight(context.Background(), 1) }()
	select {
	case <-factoryStarted:
	case <-time.After(time.Second):
		t.Fatal("lazy mouse factory did not start")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- b.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned while mouse creation was active: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(factoryRelease)

	select {
	case err := <-scrollDone:
		if !errors.Is(err, errUinputBackendClosed) {
			t.Fatalf("ScrollRight during Close = %v, want backend-closed error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ScrollRight did not finish after mouse creation release")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after mouse creation release")
	}
	if b.mouse != nil {
		t.Fatal("mouse created after Close remained retained")
	}
	if events := mouse.eventsSnapshot(); len(events) != 1 || events[0] != "close" {
		t.Fatalf("created mouse events = %v, want one deterministic close", events)
	}
}

func TestUinputOperationsRejectAfterClose(t *testing.T) {
	b, _, _ := newUinputActionBackend()
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "KeyDown", call: func() error { return b.KeyDown(context.Background(), "a") }},
		{name: "KeyUp", call: func() error { return b.KeyUp(context.Background(), "a") }},
		{name: "Type", call: func() error { return b.Type(context.Background(), "a") }},
		{name: "TypeLiteral", call: func() error { return b.TypeLiteral(context.Background(), "a") }},
		{name: "MouseMove", call: func() error { return b.MouseMove(context.Background(), 1, 2) }},
		{name: "MouseClick", call: func() error { return b.MouseClick(context.Background(), 1, 2, 1) }},
		{name: "MouseDown", call: func() error { return b.MouseDown(context.Background(), 1) }},
		{name: "MouseUp", call: func() error { return b.MouseUp(context.Background(), 1) }},
		{name: "ScrollUp", call: func() error { return b.ScrollUp(context.Background(), 1) }},
		{name: "ScrollDown", call: func() error { return b.ScrollDown(context.Background(), 1) }},
		{name: "ScrollLeft", call: func() error { return b.ScrollLeft(context.Background(), 1) }},
		{name: "ScrollRight", call: func() error { return b.ScrollRight(context.Background(), 1) }},
		{name: "PointerLocation", call: func() error {
			_, _, err := b.PointerLocation(context.Background())
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, errUinputBackendClosed) {
				t.Fatalf("%s after Close = %v, want backend-closed error", tt.name, err)
			}
		})
	}
}

func TestUinputCloseWaitsForActiveMouseUse(t *testing.T) { //nolint:gocyclo // exercises concurrent close, active use, and repeated teardown.
	kb := &recordingKeyboard{}
	tp := &recordingTouchPad{}
	started := make(chan struct{})
	mouse := &blockingMouse{
		started:     started,
		release:     make(chan struct{}),
		startedOnce: sync.OnceFunc(func() { close(started) }),
	}
	b := &UinputBackend{kb: kb, touchpad: tp, charToRune: qwertyRuneMap()}

	var factoryMu sync.Mutex
	factoryCalls := 0
	oldFactory := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) {
		factoryMu.Lock()
		factoryCalls++
		factoryMu.Unlock()
		return mouse, nil
	}
	t.Cleanup(func() { createUinputMouse = oldFactory })

	scrollDone := make(chan error, 1)
	go func() { scrollDone <- b.ScrollRight(context.Background(), 1) }()
	select {
	case <-mouse.started:
	case <-time.After(time.Second):
		t.Fatal("mouse use did not start")
	}

	closeResults := make(chan error, 2)
	go func() { closeResults <- b.Close() }()
	go func() { closeResults <- b.Close() }()
	select {
	case err := <-closeResults:
		t.Fatalf("Close returned while mouse use was active: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if events := mouse.eventsSnapshot(); len(events) != 1 {
		t.Fatalf("mouse events before release = %v, want only the wheel event", events)
	}

	close(mouse.release)
	select {
	case err := <-scrollDone:
		// Close cancels admitted work even when the last device write
		// finishes successfully during shutdown. This must not be
		// misreported as an uncanceled successful scroll.
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ScrollRight after concurrent Close: got %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ScrollRight did not finish after release")
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-closeResults:
			if err != nil {
				t.Fatalf("Close %d: %v", i, err)
			}
		case <-time.After(time.Second):
			t.Fatal("Close did not finish after active use ended")
		}
	}

	factoryMu.Lock()
	if factoryCalls != 1 {
		t.Fatalf("mouse factory calls = %d, want 1", factoryCalls)
	}
	factoryMu.Unlock()
	events := mouse.eventsSnapshot()
	if got := len(events); got != 2 || events[1] != "close" {
		t.Fatalf("mouse events after Close = %v, want wheel followed by one close", events)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if err := b.ScrollRight(context.Background(), 1); !errors.Is(err, errUinputBackendClosed) {
		t.Fatalf("ScrollRight after Close = %v, want backend-closed error", err)
	}
}

func TestUinputLazyMouseCreationFailureIsRetryable(t *testing.T) {
	kb := &recordingKeyboard{}
	tp := &recordingTouchPad{}
	m := &recordingMouse{}
	b := &UinputBackend{kb: kb, touchpad: tp, charToRune: qwertyRuneMap()}
	wantErr := errors.New("create mouse failed")
	calls := 0
	oldFactory := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) {
		calls++
		if calls == 1 {
			return nil, wantErr
		}
		return m, nil
	}
	t.Cleanup(func() { createUinputMouse = oldFactory })

	if err := b.ScrollRight(context.Background(), 1); !errors.Is(err, wantErr) {
		t.Fatalf("first ScrollRight = %v, want factory error", err)
	}
	if b.mouse != nil {
		t.Fatal("failed mouse creation populated backend state")
	}
	if err := b.ScrollRight(context.Background(), 1); err != nil {
		t.Fatalf("retry ScrollRight: %v", err)
	}
	if calls != 2 {
		t.Fatalf("mouse factory calls = %d, want 2", calls)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestQwertyRuneMapCompleteness(t *testing.T) { //nolint:gocyclo
	m := qwertyRuneMap()

	// All lowercase letters
	for _, c := range "abcdefghijklmnopqrstuvwxyz" {
		if _, ok := m[c]; !ok {
			t.Errorf("qwertyRuneMap missing lowercase %q", string(c))
		}
	}
	// All uppercase letters
	for _, c := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		if _, ok := m[c]; !ok {
			t.Errorf("qwertyRuneMap missing uppercase %q", string(c))
		}
	}
	// All digits
	for _, c := range "0123456789" {
		if _, ok := m[c]; !ok {
			t.Errorf("qwertyRuneMap missing digit %q", string(c))
		}
	}
	// Common symbols
	for _, c := range " !\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~" {
		if _, ok := m[c]; !ok {
			t.Errorf("qwertyRuneMap missing symbol %q", string(c))
		}
	}
	// Whitespace
	for _, c := range []rune{' ', '\t', '\n'} {
		if _, ok := m[c]; !ok {
			t.Errorf("qwertyRuneMap missing whitespace %q", string(c))
		}
	}

	// Verify shift correctness for a few key entries
	if m['a'].shift {
		t.Error("lowercase 'a' should not require shift")
	}
	if !m['A'].shift {
		t.Error("uppercase 'A' should require shift")
	}
	if m['1'].shift {
		t.Error("digit '1' should not require shift")
	}
	if !m['!'].shift {
		t.Error("symbol '!' should require shift")
	}
	if m[' '].shift {
		t.Error("space should not require shift")
	}
}

func TestQwertyRuneMapKeycodesAreValid(t *testing.T) {
	m := qwertyRuneMap()

	// Every entry should have a non-zero keycode
	for r, kc := range m {
		if kc.keycode <= 0 {
			t.Errorf("rune %q has invalid keycode %d", string(r), kc.keycode)
		}
	}
}

func TestBuildKernelRuneMapFallback(t *testing.T) {
	// buildKernelRuneMap may or may not succeed depending on whether
	// /dev/tty0 is accessible in this environment. Either way, the
	// backend should still be usable via the QWERTY fallback.
	m, err := buildKernelRuneMap()
	if err != nil {
		// No accessible console — verify the fallback works
		m = qwertyRuneMap()
	}

	// The map (whichever source) should at minimum contain basic ASCII
	for _, c := range "abcdefghijklmnopqrstuvwxyz" {
		if _, ok := m[c]; !ok {
			t.Errorf("rune map missing lowercase %q (err=%v)", string(c), err)
		}
	}
	for _, c := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		if _, ok := m[c]; !ok {
			t.Errorf("rune map missing uppercase %q (err=%v)", string(c), err)
		}
	}
	for _, c := range "0123456789" {
		if _, ok := m[c]; !ok {
			t.Errorf("rune map missing digit %q (err=%v)", string(c), err)
		}
	}
}

func TestKernelRuneExtraction(t *testing.T) {
	tests := []struct {
		sym    uint16
		want   rune
		wantOK bool
	}{
		// KT_LATIN (type=0) with ASCII value
		{0x0061, 'a', true}, // KT_LATIN + 'a'
		{0x0041, 'A', true}, // KT_LATIN + 'A'
		{0x0030, '0', true}, // KT_LATIN + '0'
		{0x0020, ' ', true}, // KT_LATIN + space
		// KT_LETTER (type=11) with value
		{0x0B61, 'a', true}, // KT_LETTER + 'a'
		{0x0B41, 'A', true}, // KT_LETTER + 'A'
		// Non-Latin types should not extract
		{0x0100, 0, false}, // KT_FN
		{0x0200, 0, false}, // KT_SPEC
		{0x0300, 0, false}, // KT_PAD
		{0x0400, 0, false}, // KT_DEAD
		{0x0500, 0, false}, // KT_CONS
		{0x0600, 0, false}, // KT_CUR
		{0x0700, 0, false}, // KT_SHIFT
		{0x0800, 0, false}, // KT_META
		{0x0900, 0, false}, // KT_ASCII
		{0x0A00, 0, false}, // KT_LOCK
		// Zero value
		{0x0000, 0, true}, // KT_LATIN + 0 → valid extraction, rune 0
	}

	for _, tt := range tests {
		got, ok := kernelRune(tt.sym)
		if ok != tt.wantOK {
			t.Errorf("kernelRune(0x%04X) ok=%v, want %v", tt.sym, ok, tt.wantOK)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("kernelRune(0x%04X) = %q, want %q", tt.sym, string(got), string(tt.want))
		}
	}
}

// failingKeyboard wraps recordingKeyboard and fails on KeyPress calls.
type failingKeyboard struct {
	recordingKeyboard
	failPress bool
	pressErr  error
	failUp    bool
	failUpKey int
	upErr     error
}

func (f *failingKeyboard) KeyPress(key int) error {
	if f.failPress {
		if f.pressErr != nil {
			return f.pressErr
		}
		return fmt.Errorf("injected KeyPress failure for key %d", key)
	}
	return f.recordingKeyboard.KeyPress(key)
}

func (f *failingKeyboard) KeyUp(key int) error {
	err := f.recordingKeyboard.KeyUp(key)
	if f.failUp && key == f.failUpKey {
		if f.upErr != nil {
			return f.upErr
		}
		return fmt.Errorf("injected KeyUp failure for key %d", key)
	}
	return err
}

// TestTypeKeyWithMods_ModifierReleasedOnKeyPressFailure verifies that modifier
// keys are released (best-effort) when the key action itself fails. Before the
// fix, the pressed modifier was leaked (never released).
func TestTypeKeyWithMods_ModifierReleasedOnKeyPressFailure(t *testing.T) {
	kb := &failingKeyboard{failPress: true}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}

	// {shift+a}: presses shift (succeeds), then KeyPress('a') fails.
	// The fix should release shift even though KeyPress failed.
	err := b.typeContext(context.Background(), "{shift+a}")
	if err == nil {
		t.Fatal("expected error from injected failure")
	}

	// Verify shift KeyDown was called.
	var downCount, upCount int
	for _, ev := range kb.events {
		if ev == fmt.Sprintf("down:%d", uinput.KeyLeftshift) {
			downCount++
		}
		if ev == fmt.Sprintf("up:%d", uinput.KeyLeftshift) {
			upCount++
		}
	}
	if downCount == 0 {
		t.Error("shift KeyDown was never called")
	}
	if upCount == 0 {
		t.Errorf("shift KeyUp was never called after failure; events: %v", kb.events)
	}
}

func TestTypeKeyWithMods_JoinsModifierCleanupFailure(t *testing.T) {
	keyErr := errors.New("synthetic key failure")
	cleanupErr := errors.New("synthetic modifier cleanup failure")
	kb := &failingKeyboard{
		failPress: true,
		pressErr:  keyErr,
		failUp:    true,
		failUpKey: uinput.KeyLeftshift,
		upErr:     cleanupErr,
	}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}

	err := b.typeContext(context.Background(), "{shift+a}")
	if !errors.Is(err, keyErr) {
		t.Fatalf("typeContext error = %v, want key error %v", err, keyErr)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("typeContext error = %v, want cleanup error %v joined", err, cleanupErr)
	}
}

func TestTypeKeyWithMods_ContinuesAfterModifierCleanupFailure(t *testing.T) {
	cleanupErr := errors.New("synthetic modifier cleanup failure")
	kb := &failingKeyboard{
		failUp:    true,
		failUpKey: uinput.KeyLeftctrl,
		upErr:     cleanupErr,
	}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}

	err := b.typeContext(context.Background(), "{ctrl+shift+a}")
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("typeContext error = %v, want cleanup error %v", err, cleanupErr)
	}
	for _, event := range kb.events {
		if event == fmt.Sprintf("up:%d", uinput.KeyLeftshift) {
			return
		}
	}
	t.Fatalf("events = %v, want Shift cleanup after Ctrl failure", kb.events)
}

func TestTypeText_JoinsShiftCleanupFailure(t *testing.T) {
	keyErr := errors.New("synthetic text key failure")
	cleanupErr := errors.New("synthetic shift cleanup failure")
	kb := &failingKeyboard{
		failPress: true,
		pressErr:  keyErr,
		failUp:    true,
		failUpKey: uinput.KeyLeftshift,
		upErr:     cleanupErr,
	}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}

	err := b.typeText(context.Background(), "A")
	if !errors.Is(err, keyErr) {
		t.Fatalf("typeText error = %v, want key error %v", err, keyErr)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("typeText error = %v, want cleanup error %v joined", err, cleanupErr)
	}
}

func TestUinputBackend_CanceledContextSuppressesKeyboardEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		run  func(*UinputBackend) error
	}{
		{
			name: "KeyDown",
			run:  func(b *UinputBackend) error { return b.KeyDown(ctx, "a") },
		},
		{
			name: "KeyUp",
			run:  func(b *UinputBackend) error { return b.KeyUp(ctx, "a") },
		},
		{
			name: "TypeContext",
			run:  func(b *UinputBackend) error { return b.typeContext(ctx, "A{ctrl+a}") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, kb := newTestBackend(t)

			if err := tt.run(b); !errors.Is(err, context.Canceled) {
				t.Fatalf("%s canceled error = %v, want context.Canceled", tt.name, err)
			}
			if len(kb.events) != 0 {
				t.Fatalf("%s emitted events with canceled context: %v", tt.name, kb.events)
			}
		})
	}
}

func TestUinputBackend_CanceledContextShortCircuitsPointerMethods(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := &UinputBackend{}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "MouseMove",
			run:  func() error { return b.MouseMove(ctx, 1, 2) },
		},
		{
			name: "MouseClick",
			run:  func() error { return b.MouseClick(ctx, 1, 2, 1) },
		},
		{
			name: "MouseDown",
			run:  func() error { return b.MouseDown(ctx, 1) },
		},
		{
			name: "MouseUp",
			run:  func() error { return b.MouseUp(ctx, 1) },
		},
		{
			name: "ScrollUp",
			run:  func() error { return b.ScrollUp(ctx, 1) },
		},
		{
			name: "ScrollDown",
			run:  func() error { return b.ScrollDown(ctx, 1) },
		},
		{
			name: "ScrollLeft",
			run:  func() error { return b.ScrollLeft(ctx, 1) },
		},
		{
			name: "ScrollRight",
			run:  func() error { return b.ScrollRight(ctx, 1) },
		},
		{
			name: "PointerLocation",
			run: func() error {
				_, _, err := b.PointerLocation(ctx)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("%s canceled error = %v, want context.Canceled", tt.name, err)
			}
		})
	}
}

func TestUinputRuntimeSettleAndDrain(t *testing.T) {
	b := &UinputBackend{readyAt: time.Now().Add(35 * time.Millisecond)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	calls := 0
	if err := b.withOperation(ctx, func(context.Context) error { calls++; return nil }); !errors.Is(err, context.DeadlineExceeded) || calls != 0 {
		t.Fatalf("premature dispatch: %v calls=%d", err, calls)
	}
	if err := b.withOperation(context.Background(), func(context.Context) error {
		if time.Now().Before(b.readyAt) {
			t.Fatal("dispatched before readiness window")
		}
		calls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || b.lastDispatch.IsZero() {
		t.Fatal("missing dispatch timestamp")
	}
	dispatched := b.lastDispatch
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Now().Before(dispatched.Add(uinputDrain)) {
		t.Fatal("device destroyed before drain window")
	}
}
