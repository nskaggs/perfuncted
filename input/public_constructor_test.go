package input

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestNewGnomeNativeBackendForRuntimeRequiresSession(t *testing.T) {
	if _, err := NewGnomeNativeBackendForRuntime(env.FromEnviron(nil)); err == nil {
		t.Fatal("NewGnomeNativeBackendForRuntime succeeded without a session bus")
	}
}
