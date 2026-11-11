//go:build integration && linux

package flatpaktest

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttendedScreenshotValidationAcceptsValidPNGAndRejectsInvalidData(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("util-linux script needed for fake attended terminal")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 needed by attended screenshot validator")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"flatpak": "#!/bin/sh\nexit 0\n",
		"pf":      "#!/bin/sh\ncat \"$PF_CAPTURE_FIXTURE\"\nexit \"${PF_CAPTURE_EXIT:-0}\"\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	valid := filepath.Join(root, "valid.png")
	f, err := os.Create(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 64, 48))); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	validBytes, err := os.ReadFile(valid)
	if err != nil {
		t.Fatal(err)
	}
	truncated := filepath.Join(root, "truncated.png")
	if err := os.WriteFile(truncated, validBytes[:len(validBytes)-12], 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), validBytes...)
	if len(corrupt) < 65 {
		t.Fatal("PNG fixture unexpectedly small")
	}
	corrupt[55] ^= 0xff
	corrupted := filepath.Join(root, "corrupted.png")
	if err := os.WriteFile(corrupted, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(root, "invalid.png")
	if err := os.WriteFile(invalid, []byte("a fake png"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mode, fixture, exit string
		wantSuccess               bool
	}{
		{"valid PNG", "capture", valid, "0", true},
		{"invalid PNG", "capture", invalid, "0", false},
		{"truncated PNG", "capture", truncated, "0", false},
		{"corrupted PNG", "capture", corrupted, "0", false},
		{"denied returns nothing", "capture-deny", filepath.Join(root, "empty"), "1", true},
		{"canceled returns nothing", "capture-cancel", filepath.Join(root, "empty"), "1", true},
		{"denied leaks partial pixels", "capture-deny", valid, "1", false},
		{"canceled leaks partial pixels", "capture-cancel", valid, "1", false},
		{"denied unexpectedly succeeds", "capture-deny", valid, "0", false},
		{"canceled unexpectedly succeeds", "capture-cancel", valid, "0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := filepath.Join(root, strings.ReplaceAll(tc.name, " ", "-"))
			if err := os.Mkdir(runtime, 0700); err != nil {
				t.Fatal(err)
			}
			// A dedicated pseudo-terminal permits the validator to run while all
			// installed desktop and Flatpak interactions are deterministic fakes.
			path := filepath.Join(mustRepoRoot(t), "scripts", "pf-flatpak-attended-check")
			cmd := exec.Command("script", "-q", "-e", "-c", "bash "+path+" "+tc.mode, "/dev/null")
			cmd.Stdin = strings.NewReader("\n")
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "PF_CAPTURE_FIXTURE="+tc.fixture, "PF_CAPTURE_EXIT="+tc.exit, "XDG_CURRENT_DESKTOP=KDE", "XDG_SESSION_TYPE=wayland", "WAYLAND_DISPLAY=wayland-fake", "XDG_RUNTIME_DIR="+runtime)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantSuccess {
				t.Fatalf("validator success=%v expected=%v output=%s", err == nil, tc.wantSuccess, out)
			}
			if tc.wantSuccess && !bytes.Contains(out, []byte("PASS:")) {
				t.Fatalf("missing success proof: %s", out)
			}
			entries, err := os.ReadDir(runtime)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("private screenshot artifacts were not removed: %v", entries)
			}
		})
	}
}
