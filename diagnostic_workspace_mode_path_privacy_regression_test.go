package perfuncted

import (
	"strings"
	"testing"
)

// An explicit GOWORK file may be located under private directories and
// include user or project identifiers. A persistent bundle should describe
// build mode, never copy that filesystem path into diagnostic metadata.
func TestDiagnosticBuildInfoRedactsExplicitGoWorkspacePath(t *testing.T) {
	const secret = "/home/private-user/projects/internal-product/workspace.go.work"
	for _, tc := range []struct{ value, want string }{
		{"", "default"},
		{"off", "off"},
		{secret, "on"},
	} {
		t.Run(tc.want+"-"+strings.ReplaceAll(tc.value, "/", "_"), func(t *testing.T) {
			t.Setenv("GOWORK", tc.value)
			info := currentBuildInfo()
			if info.WorkspaceMode != tc.want {
				t.Fatalf("workspace mode=%q, want %q", info.WorkspaceMode, tc.want)
			}
			if strings.Contains(info.WorkspaceMode, secret) {
				t.Fatalf("workspace path exposed in diagnostic: %q", info.WorkspaceMode)
			}
		})
	}
}
