//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/env"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunGSettingsIsolatedRuntimeEnvironmentAndExitStatus(t *testing.T) {
	bin := t.TempDir()
	script := filepath.Join(bin, "gsettings")
	contents := "#!/bin/sh\nprintf 'state=%s arguments=%s' \"$PF_SYNTHETIC_GSETTINGS_STATE\" \"$*\"\nif [ \"$1\" = fail ]; then printf ' denied' >&2; exit 23; fi\n"
	if err := os.WriteFile(script, []byte(contents), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PF_SYNTHETIC_GSETTINGS_STATE", "host-secret-must-not-leak")
	rt := envRuntimeForGSettings(bin, "runtime-scope")
	out, err := runGSettings(context.Background(), rt, "get", "org.gnome.shell", "enabled-extensions")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "state=runtime-scope arguments=get org.gnome.shell enabled-extensions" {
		t.Fatalf("wrong subprocess environment or arguments: %q", got)
	}
	out, err = runGSettings(context.Background(), rt, "fail", "org.gnome.shell")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 || !strings.Contains(string(out), "denied") {
		t.Fatalf("failed command: out=%q err=%v", out, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runGSettings(canceled, rt, "get", "org.gnome.shell")
	if err == nil {
		t.Fatal("canceled context launched gsettings without an error")
	}
}

func TestAtomicReplaceDirectoryRejectsWrongParentAndMissingSource(t *testing.T) {
	root := t.TempDir()
	one := filepath.Join(root, "one")
	two := filepath.Join(root, "two")
	if err := os.Mkdir(one, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(two, 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicReplaceDirectory(filepath.Join(one, "staging"), filepath.Join(two, "installed")); err == nil || !strings.Contains(err.Error(), "shared parent") {
		t.Fatalf("cross-directory replacement accepted: %v", err)
	}
	if err := atomicReplaceDirectory(filepath.Join(one, "missing"), filepath.Join(one, "installed")); err == nil {
		t.Fatal("missing staged extension accepted")
	}
}

func TestEmbeddedGNOMEExtensionNeverWritesThroughInvalidDestination(t *testing.T) {
	file := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(file, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeEmbeddedExtension(file); err == nil || !strings.Contains(err.Error(), "write") {
		t.Fatalf("non-directory destination accepted: %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("failed write altered original: %q %v", data, err)
	}
}

func envRuntimeForGSettings(bin, marker string) env.Runtime {
	return env.FromEnviron([]string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "PF_SYNTHETIC_GSETTINGS_STATE=" + marker})
}
