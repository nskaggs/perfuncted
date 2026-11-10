//go:build integration && linux

package flatpaktest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlatpakHostLauncher(t *testing.T) {
	repo := mustRepoRoot(t)
	script := filepath.Join(repo, "scripts", "pf-flatpak")
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	// Mock the flatpak executable to prove that forwarding, quoting, and
	// path grants work without operating on a real desktop.
	mock := filepath.Join(bin, "flatpak")
	if err := os.WriteFile(mock, []byte("#!/bin/sh\nprintf '<%s>\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	granted := filepath.Join(tmp, "host files with spaces")
	if err := os.MkdirAll(granted, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	t.Run("regular CLI arguments", func(t *testing.T) {
		out, err := run("info", "--output=json")
		if err != nil || out != "<run>\n<io.github.nskaggs.perfuncted>\n<info>\n<--output=json>\n" {
			t.Fatalf("launcher forwarded wrong arguments: %q, %v", out, err)
		}
	})
	t.Run("read-only host input", func(t *testing.T) {
		out, err := run("--host-dir-ro", granted, "run", filepath.Join(granted, "script with spaces.pf"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "<--filesystem="+granted+":ro>") {
			t.Errorf("read-only scope missing: %q", out)
		}
		if !strings.Contains(out, "<"+filepath.Join(granted, "script with spaces.pf")+">") {
			t.Errorf("script argument was mangled: %q", out)
		}
	})
	t.Run("writable output scope", func(t *testing.T) {
		out, err := run("--host-dir", granted, "screen", "grab", "--out", filepath.Join(granted, "out.png"))
		if err != nil || !strings.Contains(out, "<--filesystem="+granted+">") {
			t.Fatalf("write scope missing: %q %v", out, err)
		}
	})
	t.Run("reject nonexistent directory", func(t *testing.T) {
		out, err := run("--host-dir-ro", filepath.Join(tmp, "missing"), "version")
		if err == nil || !strings.Contains(out, "existing host directory") {
			t.Fatalf("bad scope accepted: %q %v", out, err)
		}
	})
	t.Run("literal dashdash stops launcher parsing", func(t *testing.T) {
		out, err := run("--", "--host-dir", "unexpected")
		if err != nil || !strings.Contains(out, "<--host-dir>") || !strings.Contains(out, "<unexpected>") {
			t.Fatalf("literal pass-through failed: %q %v", out, err)
		}
	})
}
