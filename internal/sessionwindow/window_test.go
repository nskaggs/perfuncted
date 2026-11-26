package sessionwindow

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/window"
)

type discoveryGate struct{ closed bool }

func (g *discoveryGate) CheckBackend(string, any) error { return g.EnsureOpen() }
func (*discoveryGate) BackendContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}
func (g *discoveryGate) EnsureOpen() error {
	if g.closed {
		return capability.ErrSessionClosed
	}
	return nil
}
func (*discoveryGate) OperationError(_ string, err error) error { return err }

type discoveryBackend struct {
	window.Manager
	reads, closes int
	after         func()
}

func (b *discoveryBackend) List(context.Context) ([]window.Info, error) {
	b.reads++
	if b.after != nil {
		b.after()
	}
	return []window.Info{{NativeID: "one", Title: "Editor"}, {NativeID: "two", Title: "Browser"}}, nil
}
func (b *discoveryBackend) Close() error { b.closes++; return nil }

func TestOwnerDiscoveryKeepsIdentityAndSuppressesRevokedResults(t *testing.T) {
	ctx := context.Background()
	gate := &discoveryGate{}
	backend := &discoveryBackend{}
	owner := New(gate, nil)
	owner.Install(backend)
	targets, err := owner.Discover(ctx, window.Match{})
	if err != nil || len(targets) != 2 {
		t.Fatalf("discovery=%v err=%v", targets, err)
	}
	if targets[0].Native() != "one" || targets[0].CachedInfo().NativeID != "one" || targets[1].Native() != "two" {
		t.Fatal("discovery lost native identity")
	}
	copied := targets[0].CachedInfo()
	copied.NativeID = "two"
	if targets[0].Native() != "one" || targets[0].CachedInfo().NativeID != "one" {
		t.Fatal("metadata copy redirected target")
	}
	backend.after = func() { gate.closed = true }
	targets, err = owner.Discover(ctx, window.Match{})
	if len(targets) != 0 || !errors.Is(err, capability.ErrSessionClosed) || backend.reads != 2 {
		t.Fatalf("revoked discovery=%v err=%v reads=%d", targets, err, backend.reads)
	}
	if err := owner.Close(); err != nil || backend.closes != 1 {
		t.Fatalf("close=%v calls=%d", err, backend.closes)
	}
}
