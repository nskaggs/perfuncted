package screen

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntimeContextX11ConnectionFailureReturnsNilScreenshotter(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=invalid-display-name"})
	capture, err := OpenRuntimeContext(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), "screen/x11: connect") {
		t.Fatalf("missing X11 connection failure: %v", err)
	}
	if capture != nil {
		t.Fatalf("failed X11 constructor leaked typed-nil screenshotter: %T", capture)
	}
}
