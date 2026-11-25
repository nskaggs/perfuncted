package clipboard

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestNewGnomeNativeClipboardForRuntimeRequiresSession(t *testing.T) {
	if _, err := NewGnomeNativeClipboardForRuntime(env.FromEnviron(nil)); err == nil {
		t.Fatal("NewGnomeNativeClipboardForRuntime succeeded without a session bus")
	}
}
