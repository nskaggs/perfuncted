//go:build linux

package input

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/bendahl/uinput"
)

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
					if path != "/dev/uinput" || string(name) != "perfuncted-touchpad" || xmin != 0 || xmax != 1440 || ymin != 0 || ymax != 900 {
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
