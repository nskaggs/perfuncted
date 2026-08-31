//go:build linux
// +build linux

package gnomebridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestParseEnabledExtensions(t *testing.T) {
	got := parseEnabledExtensions("@as ['one@example', 'two@example']")
	want := []string{"one@example", "two@example"}
	if !slices.Equal(got, want) {
		t.Fatalf("parseEnabledExtensions = %v, want %v", got, want)
	}
	if got := parseEnabledExtensions("[]"); len(got) != 0 {
		t.Fatalf("parseEnabledExtensions([]) = %v", got)
	}
}

func TestInstallerWritesBundledExtensionAndPreservesEnabledList(t *testing.T) {
	dataHome := t.TempDir()
	var calls [][]string
	runner := func(_ context.Context, _ env.Runtime, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "get" {
			return []byte("['other@example']"), nil
		}
		return nil, nil
	}
	installer := Installer{DataHome: dataHome, Run: runner}
	path, err := installer.Install(context.Background(), env.FromEnviron(nil))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if want := filepath.Join(dataHome, extensionDirectory); path != want {
		t.Fatalf("Install path = %q, want %q", path, want)
	}
	for _, name := range []string{"metadata.json", "extension.js", "service.js", "windows.js", "screen.js", "input.js", "clipboard.js"} {
		if _, err := os.Stat(filepath.Join(path, name)); err != nil {
			t.Fatalf("installed %s: %v", name, err)
		}
	}
	if len(calls) != 2 || calls[1][0] != "set" {
		t.Fatalf("gsettings calls = %v, want get and set", calls)
	}
	if !strings.Contains(calls[1][3], "other@example") || !strings.Contains(calls[1][3], extensionUUID) {
		t.Fatalf("enabled-extensions value = %q, want both extensions", calls[1][3])
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), extensionUUID+".old")); !os.IsNotExist(err) {
		t.Fatalf("old extension backup remains: %v", err)
	}
}

func TestInstallerDoesNotRewriteEnabledSettingWhenAlreadyEnabled(t *testing.T) {
	var calls [][]string
	runner := func(_ context.Context, _ env.Runtime, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return []byte("['" + extensionUUID + "']"), nil
	}
	installer := Installer{DataHome: t.TempDir(), Run: runner}
	if _, err := installer.Install(context.Background(), env.FromEnviron(nil)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(calls) != 1 || calls[0][0] != "get" {
		t.Fatalf("gsettings calls = %v, want only get", calls)
	}
}

func TestInstallerRejectsFlatpakHostProvisioning(t *testing.T) {
	_, err := NewInstallerForRuntime(env.FromEnviron([]string{
		"FLATPAK_ID=io.github.nskaggs.perfuncted",
		"HOME=/home/user",
		"XDG_DATA_HOME=/home/user/.var/app/io.github.nskaggs.perfuncted/data",
	}))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("NewInstallerForRuntime error = %v, want ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "native perfuncted package") {
		t.Fatalf("NewInstallerForRuntime error = %v, want actionable Flatpak message", err)
	}
}
