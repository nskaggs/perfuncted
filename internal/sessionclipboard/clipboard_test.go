package sessionclipboard

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/v2/internal/capability"
)

type clipboardGate struct {
	closed bool
	deny   string
}

func (g *clipboardGate) CheckBackend(op string, _ any) error {
	if g.closed {
		return capability.ErrSessionClosed
	}
	if op == g.deny {
		return capability.ErrUnsupported
	}
	return nil
}
func (*clipboardGate) BackendContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}
func (*clipboardGate) Trace(string, string) {}
func (g *clipboardGate) EnsureOpen() error {
	if g.closed {
		return capability.ErrSessionClosed
	}
	return nil
}
func (*clipboardGate) OperationError(_ string, err error) error { return err }

type clipboardFixture struct {
	reads, writes, closes int
	value                 string
	after                 func()
}

func (b *clipboardFixture) Get(context.Context) (string, error) {
	b.reads++
	if b.after != nil {
		b.after()
	}
	return b.value, nil
}
func (b *clipboardFixture) Set(_ context.Context, value string) error {
	b.writes++
	b.value = value
	if b.after != nil {
		b.after()
	}
	return nil
}
func (b *clipboardFixture) Close() error { b.closes++; return nil }

func TestOwnerDispatchAndRevocation(t *testing.T) {
	ctx := context.Background()
	gate := &clipboardGate{}
	backend := &clipboardFixture{value: "private"}
	owner := New(gate, nil)
	owner.Install(backend)
	if got, err := owner.Get(ctx); err != nil || got != "private" {
		t.Fatalf("read=%q err=%v", got, err)
	}
	gate.deny = "set"
	if err := owner.Set(ctx, "denied"); !errors.Is(err, capability.ErrUnsupported) || backend.writes != 0 {
		t.Fatalf("denied mutation=%v writes=%d", err, backend.writes)
	}
	gate.deny = ""
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got, err := owner.Get(canceled); got != "" || !errors.Is(err, context.Canceled) || backend.reads != 1 {
		t.Fatalf("pre-canceled read=%q err=%v calls=%d", got, err, backend.reads)
	}
	backend.after = func() { gate.closed = true }
	if got, err := owner.Get(ctx); got != "" || !errors.Is(err, capability.ErrSessionClosed) || backend.reads != 2 {
		t.Fatalf("late read=%q err=%v calls=%d", got, err, backend.reads)
	}
	gate.closed = false
	if err := owner.Set(ctx, "written"); !errors.Is(err, capability.ErrSessionClosed) || backend.writes != 1 {
		t.Fatalf("late mutation=%v calls=%d", err, backend.writes)
	}
	if err := owner.Close(); err != nil || backend.closes != 1 {
		t.Fatalf("close=%v calls=%d", err, backend.closes)
	}
}
