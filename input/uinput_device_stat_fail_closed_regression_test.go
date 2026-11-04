//go:build linux

package input

import (
	"errors"
	"os"
	"testing"

	"github.com/bendahl/uinput"
)

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
