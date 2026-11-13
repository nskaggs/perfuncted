package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/output"
)

type countOutputReadBackend struct{ calls int }

func (b *countOutputReadBackend) List(context.Context) ([]output.Info, error) {
	b.calls++
	return []output.Info{{Name: "PRIVATE"}}, nil
}
func (b *countOutputReadBackend) Close() error { return nil }

type countClipboardReadBackend struct{ calls int }

func (b *countClipboardReadBackend) Get(context.Context) (string, error) {
	b.calls++
	return "private", nil
}
func (b *countClipboardReadBackend) Set(context.Context, string) error { return nil }
func (b *countClipboardReadBackend) Close() error                      { return nil }

// A canceled request must not initiate a private read, even when the backend
// ignores the passed context and produces a result that is later discarded.
func TestDesktopReadFacadesDoNotDispatchAfterPreCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outputs := &countOutputReadBackend{}
	clipboard := &countClipboardReadBackend{}
	session := NewSessionForTesting(nil, nil, nil, outputs, clipboard)
	t.Cleanup(func() { _ = session.Close() })
	infos, err := session.Outputs.List(ctx)
	if !errors.Is(err, context.Canceled) || len(infos) > 0 || outputs.calls != 0 {
		t.Errorf("canceled output request leaked %v, err %v, backend calls %d", infos, err, outputs.calls)
	}
	value, err := session.Clipboard.Get(ctx)
	if !errors.Is(err, context.Canceled) || value != "" || clipboard.calls != 0 {
		t.Errorf("canceled clipboard request leaked %q, err %v, backend calls %d", value, err, clipboard.calls)
	}
}
