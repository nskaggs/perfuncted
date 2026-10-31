//go:build linux

package gnomebridge

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// A malformed caller must never panic or create files when requesting
// privileged GNOME Shell extension provisioning.
func TestGNOMEInstallerRejectsNilContextWithoutFilesystemMutation(t *testing.T) {
	dir := t.TempDir()
	runnerCalled := false
	installer := Installer{DataHome: dir, Run: func(context.Context, env.Runtime, ...string) ([]byte, error) { runnerCalled = true; return nil, nil }}
	_, err := installer.Install(absentInstallerContext, env.FromEnviron(nil))
	if err == nil || !strings.Contains(err.Error(), "nil context") || runnerCalled {
		t.Fatalf("nil-context provisioning accepted; err=%v runner=%t", err, runnerCalled)
	}
}

// An intentionally absent context exercises the public nil-context boundary.
var absentInstallerContext context.Context
