//go:build integration && linux

package flatpaktest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlatpakSourceSnapshotDoesNotCreateWorktreeOrTouchCheckoutBuildArtifacts(t *testing.T) {
	repo := mustRepoRoot(t)
	root := t.TempDir()
	source := filepath.Join(root, "source")
	worktrees := func() string {
		t.Helper()
		out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	before := worktrees()
	sentinel := filepath.Join(root, "unrelated-builddir", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := snapshotFlatpakSource(repo, source); err != nil {
		t.Fatal(err)
	}
	if before != worktrees() {
		t.Fatal("snapshot changed registered Git worktrees")
	}
	if _, err := os.Stat(filepath.Join(source, ".git")); !os.IsNotExist(err) {
		t.Fatalf("snapshot must not be a Git worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, "io.github.nskaggs.perfuncted.yml")); err != nil {
		t.Fatalf("manifest absent from source snapshot: %v", err)
	}
	if err := writeFlatpakBuildInfo(repo, source); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(source, "flatpak", "build-info.txt"))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), strings.TrimSpace(string(revision))) {
		t.Fatalf("snapshot metadata not stamped from checked-out revision: %q", raw)
	}
	raw, err = os.ReadFile(sentinel)
	if err != nil || string(raw) != "preserve" {
		t.Fatalf("unrelated build artifacts changed: %s %v", raw, err)
	}
}
