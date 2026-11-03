package window

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntimeContextX11ConnectionFailureReturnsNilManager(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=invalid-display-name"})
	manager, err := OpenRuntimeContext(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), "window/x11: connect") {
		t.Fatalf("missing X11 connection failure: %v", err)
	}
	if manager != nil {
		t.Fatalf("failed X11 constructor leaked typed-nil manager: %T", manager)
	}
}
