package perfuncted

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/window"
)

type closeCountingScreen struct {
	capabilityScreen
	calls atomic.Int32
}

func (s *closeCountingScreen) Close() error {
	s.calls.Add(1)
	return s.capabilityScreen.Close()
}

type closeCountingInput struct {
	calls atomic.Int32
}

func (*closeCountingInput) KeyDown(context.Context, string) error { return nil }
func (*closeCountingInput) KeyUp(context.Context, string) error   { return nil }
func (*closeCountingInput) Type(context.Context, string) error    { return nil }
func (*closeCountingInput) TypeLiteral(context.Context, string) error {
	return nil
}
func (*closeCountingInput) MouseMove(context.Context, int, int) error { return nil }
func (*closeCountingInput) MouseClick(context.Context, int, int, int) error {
	return nil
}
func (*closeCountingInput) MouseDown(context.Context, int) error { return nil }
func (*closeCountingInput) MouseUp(context.Context, int) error   { return nil }
func (*closeCountingInput) ScrollUp(context.Context, int) error  { return nil }
func (*closeCountingInput) ScrollDown(context.Context, int) error {
	return nil
}
func (*closeCountingInput) ScrollLeft(context.Context, int) error { return nil }
func (*closeCountingInput) ScrollRight(context.Context, int) error {
	return nil
}
func (*closeCountingInput) PointerLocation(context.Context) (int, int, error) {
	return 0, 0, input.ErrNotSupported
}
func (*closeCountingInput) Sync(context.Context) error { return nil }
func (s *closeCountingInput) Close() error {
	s.calls.Add(1)
	return nil
}

var (
	_ input.Inputter = (*closeCountingInput)(nil)
	_ window.Manager = (*listOnlyWindowManager)(nil)
	_ output.Lister  = (*closeCountingOutput)(nil)
	_ input.Inputter = (*closeCountingInput)(nil)
)

type closeCountingOutput struct {
	calls atomic.Int32
}

func (o *closeCountingOutput) List(context.Context) ([]output.Info, error) {
	return nil, nil
}
func (o *closeCountingOutput) Close() error {
	o.calls.Add(1)
	return nil
}

func TestSessionCloseUnifiesBundleTeardown(t *testing.T) {
	t.Parallel()
	screenBackend := &closeCountingScreen{}
	inputBackend := &closeCountingInput{}
	windowBackend := &listOnlyWindowManager{}
	outputBackend := &closeCountingOutput{}
	clipboardBackend := &capabilityClipboard{}
	session := NewSessionForTesting(screenBackend, inputBackend, windowBackend, outputBackend, clipboardBackend)
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := screenBackend.calls.Load(); got != 1 {
		t.Fatalf("screen close calls = %d, want 1", got)
	}
	if got := inputBackend.calls.Load(); got != 1 {
		t.Fatalf("input close calls = %d, want 1", got)
	}
	if got := outputBackend.calls.Load(); got != 1 {
		t.Fatalf("output close calls = %d, want 1", got)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := screenBackend.calls.Load(); got != 1 {
		t.Fatalf("screen close calls after second Close = %d, want 1", got)
	}
	if got := inputBackend.calls.Load(); got != 1 {
		t.Fatalf("input close calls after second Close = %d, want 1", got)
	}
}

func TestBundleCloseNilSafe(t *testing.T) {
	t.Parallel()
	var screenBundle *ScreenBundle
	if err := screenBundle.close(); err != nil {
		t.Fatalf("nil ScreenBundle close = %v, want nil", err)
	}
	var inputBundle *InputBundle
	if err := inputBundle.close(); err != nil {
		t.Fatalf("nil InputBundle close = %v, want nil", err)
	}
}
