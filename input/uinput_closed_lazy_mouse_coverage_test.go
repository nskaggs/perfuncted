//go:build linux

package input

import (
	"errors"
	"testing"

	"github.com/bendahl/uinput"
)

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
