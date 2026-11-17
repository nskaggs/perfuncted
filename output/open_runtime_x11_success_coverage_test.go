//go:build linux

package output

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/x11test"
)

func TestOpenRuntimeContextSelectsAndListsLiveX11Outputs(t *testing.T) {
	authority := filepath.Join(t.TempDir(), "empty-xauthority")
	if err := os.WriteFile(authority, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", authority)
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Skipf("isolated Xvfb unavailable: %v", err)
	}
	t.Cleanup(stop)

	lister, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{"DISPLAY=" + display}))
	if err != nil {
		t.Fatalf("open output runtime on Xvfb: %v", err)
	}
	x11Lister, ok := lister.(*X11Lister)
	if !ok || x11Lister == nil {
		t.Fatalf("output runtime returned %T, want live *X11Lister", lister)
	}
	t.Cleanup(func() { _ = lister.Close() })

	outputs, err := lister.List(context.Background())
	if err != nil || len(outputs) == 0 {
		t.Fatalf("list Xvfb outputs: %+v, %v", outputs, err)
	}
	for _, output := range outputs {
		if output.Backend != "x11" || !output.Available || output.Geometry.W <= 0 || output.Geometry.H <= 0 {
			t.Fatalf("invalid Xvfb output: %+v", output)
		}
	}
}
