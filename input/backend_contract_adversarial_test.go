package input

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/env"
	"os"
	"strings"
	"testing"
)

func TestBackendConstructorReturningNilNilMustNotBeAccepted(t *testing.T) {
	oW, oX, oU, oStat := newWlVirtualBackend, newXTestBackend, newUinputBackend, statUinput
	t.Cleanup(func() { newWlVirtualBackend = oW; newXTestBackend = oX; newUinputBackend = oU; statUinput = oStat })
	newWlVirtualBackend = func(context.Context, string) (Inputter, error) { return nil, nil }
	newUinputBackend = func(int32, int32) (Inputter, error) { return backendName{name: "uinput"}, nil }
	newXTestBackend = func(string) (Inputter, error) { return backendName{name: "xtest"}, nil }
	statUinput = func() error { return nil }
	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{"DISPLAY=:99", "WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR=" + t.TempDir()})
	b, err := OpenRuntimeContext(context.Background(), rt, 1600, 900)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil {
		t.Fatal("successful open returned nil Inputter")
	}
	if got := b.(backendName).name; got != "uinput" {
		t.Fatalf("expected fallback after nil backend, got %s", got)
	}
}

func TestCanceledSuccessfulBackendConstructionMustCloseBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := false
	b, err := openInputCandidate(ctx, new(inputAttempts), "delayed", func() (Inputter, error) {
		cancel()
		return &closeTrackingBackend{onClose: func() { closed = true }}, nil
	})
	if b != nil || !errors.Is(err, context.Canceled) || !closed {
		t.Fatalf("b=%v err=%v closed=%v", b, err, closed)
	}
}

type closeTrackingBackend struct {
	noopInputter
	onClose func()
}

func (b *closeTrackingBackend) Close() error { b.onClose(); return nil }

func TestForcedBackendNilDeviceMustReturnUsefulError(t *testing.T) {
	old, oldStat := newUinputBackend, statUinput
	t.Cleanup(func() { newUinputBackend = old; statUinput = oldStat })
	newUinputBackend = func(int32, int32) (Inputter, error) { return nil, nil }
	statUinput = func() error { return nil }
	t.Setenv("PF_FORCE_INPUT", "uinput")
	b, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{"DISPLAY=:99"}), 1600, 900)
	if b != nil || err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("nil backend accepted, b=%v err=%v", b, err)
	}
}

var _ = os.ErrNotExist

func TestUinputConstructorReturningDeviceAndErrorClosesDevice(t *testing.T) {
	old := newUinputBackend
	t.Cleanup(func() { newUinputBackend = old })
	closed := 0
	newUinputBackend = func(int32, int32) (Inputter, error) {
		return &closeTrackingBackend{onClose: func() { closed++ }}, errors.New("constructor half failed")
	}
	b, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 1600, 900)
	if b != nil || err == nil || closed != 1 {
		t.Fatalf("backend=%v err=%v close calls=%d, want 1", b, err, closed)
	}
}
func TestOpenInputCandidateWithBackendAndErrorClosesOnce(t *testing.T) {
	closed := 0
	attempts := inputAttempts{}
	b, err := openInputCandidate(context.Background(), &attempts, "corrupt", func() (Inputter, error) {
		return &closeTrackingBackend{onClose: func() { closed++ }}, errors.New("broken")
	})
	if b != nil || err == nil || closed != 1 || len(attempts) != 1 {
		t.Fatalf("b=%v err=%v closed=%d errors=%d", b, err, closed, len(attempts))
	}
}
