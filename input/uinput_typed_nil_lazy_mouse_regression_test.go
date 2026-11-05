//go:build linux

package input

import (
	"github.com/bendahl/uinput"
	"github.com/nskaggs/perfuncted/internal/util"
	"strings"
	"testing"
)

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
