//go:build linux

package clipboard

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestGnomeNativeUninitializedBoundary(t *testing.T) {
	for _, cb := range []*GnomeNativeClipboard{nil, {}} {
		if text, err := cb.Get(context.Background()); err == nil || text != "" || !strings.Contains(err.Error(), "not initialised") {
			t.Fatalf("Get = %q, %v", text, err)
		}
		if err := cb.Set(context.Background(), "private clipboard text"); err == nil || !strings.Contains(err.Error(), "not initialised") {
			t.Fatalf("Set error = %v", err)
		}
		if err := cb.Close(); err != nil {
			t.Fatalf("Close = %v", err)
		}
	}
}

func TestClipboardCapabilitiesAreIsolated(t *testing.T) {
	native := (*GnomeNativeClipboard)(nil)
	cmd := (*extCmdClipboard)(nil)
	a, b := native.SupportedOperations(), cmd.SupportedOperations()
	if len(a) == 0 || !reflect.DeepEqual(a, b) {
		t.Fatalf("inconsistent operations: %v %v", a, b)
	}
	a[0] = "injected"
	if reflect.DeepEqual(a, native.SupportedOperations()) {
		t.Fatal("caller mutated global supported operations")
	}
}

func TestGnomeNativeCanceledConstructor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cb, err := NewGnomeNativeClipboardForRuntimeContext(ctx, env.FromEnviron(nil))
	if cb != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("constructor = %v, %v", cb, err)
	}
}
