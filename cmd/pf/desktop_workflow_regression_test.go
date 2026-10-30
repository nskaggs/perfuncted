package main

import (
	"context"
	"github.com/nskaggs/perfuncted"
	"path/filepath"
	"strings"
	"testing"
)

func TestScreenshotClickCommandRequiresOutputs(t *testing.T) {
	root := newRootCmd(func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return nil, nil }
	})
	if findCommandPath(root, "input", "click-screen") == nil {
		t.Fatal("missing screenshot-to-logical click command")
	}
}
func TestFileChooserCommandRequiresARealFileBeforeOpeningSession(t *testing.T) {
	opened := false
	factory := func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { opened = true; return nil, nil }
	}
	for _, path := range []string{"relative.epub", filepath.Join(t.TempDir(), "missing.epub")} {
		_, errOut, code := captureRunIO(t, []string{"input", "select-file", "--title", "File Upload", "--path", path}, factory)
		if code == 0 || opened {
			t.Fatalf("path=%q code=%d opened=%v stderr=%q", path, code, opened, errOut)
		}
	}
}
func TestInfoWarningMentionsExplicitConsent(t *testing.T) {
	report := infoReport{Compositor: "KDE Plasma Wayland", Capabilities: map[string]capabilityEntry{"input": {Supported: true, Backend: "*input.XTestBackend"}}}
	warning := inputBackendWarning(report)
	if !strings.Contains(warning, "remote-desktop") || !strings.Contains(warning, "native Wayland") {
		t.Fatalf("warning=%q", warning)
	}
}
