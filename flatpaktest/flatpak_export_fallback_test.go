//go:build integration && linux

package flatpaktest

import "testing"

func TestFlatpakIconExportFallbackRequiresExactNestedSandboxFailure(t *testing.T) {
	for _, tc := range []struct {
		name, log string
		want      bool
	}{
		{name: "nested validator has no loader",
			log:  "error: /tmp/builddir/export/share/icons/hicolor/128x128/apps/pf.png is not a valid icon: Format not recognized\n\nExport failed: Child process exited with code 1",
			want: true},
		{name: "ordinary compilation error", log: "go: inconsistent vendoring\nExport failed: Child process exited with code 1"},
		{name: "missing required runtime", log: "error: runtime not installed\nExport failed: Child process exited with code 1"},
		{name: "invalid icon dimensions", log: "error: expected a square icon, got 128x120\nExport failed: Child process exited with code 1"},
		{name: "loader phrase without export error", log: "error: invalid icon: Format not recognized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsUnsandboxedIconExport(tc.log); got != tc.want {
				t.Fatalf("allow fallback=%t, want %t: %q", got, tc.want, tc.log)
			}
		})
	}
}
