package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareRejectsDrift(t *testing.T) {
	want, got := t.TempDir(), t.TempDir()
	writeFixture(t, want, "file.go", "same")
	writeFixture(t, got, "file.go", "same")
	if err := compare(want, got); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, got, "file.go", "changed")
	if err := compare(want, got); err == nil || !strings.Contains(err.Error(), "file.go") {
		t.Fatalf("changed source: %v", err)
	}
	writeFixture(t, got, "file.go", "same")
	writeFixture(t, got, "extra.go", "extra")
	if err := compare(want, got); err == nil {
		t.Fatal("extra source accepted")
	}
}

func TestNormalizePreservesNonGoFiles(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module "+upstreamPath)
	writeFixture(t, root, "source.go", "import \""+upstreamPath+"/xproto\"")
	writeFixture(t, root, "LICENSE", upstreamPath)
	if err := normalize(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); !os.IsNotExist(err) {
		t.Fatalf("nested go.mod remains: %v", err)
	}
	for name, want := range map[string]string{"source.go": "import \"" + internalPath + "/xproto\"", "LICENSE": upstreamPath} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(b) != want {
			t.Fatalf("%s: %q, %v", name, b, err)
		}
	}
}

func TestPatchConflictLeavesSourceIntact(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "file", "actual\n")
	patch := filepath.Join(t.TempDir(), "change.patch")
	if err := os.WriteFile(patch, []byte("diff --git a/base/file b/next/file\n--- a/base/file\n+++ b/next/file\n@@ -1 +1 @@\n-expected\n+replacement\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := applyPatch(root, patch); err == nil {
		t.Fatal("conflict accepted")
	}
	data, err := os.ReadFile(filepath.Join(root, "file"))
	if err != nil || string(data) != "actual\n" {
		t.Fatalf("source changed on conflict: %q, %v", data, err)
	}
}

func TestInstallReplacesOnlyGeneratedTree(t *testing.T) {
	parent := t.TempDir()
	target, source := filepath.Join(parent, "xgb"), t.TempDir()
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, target, "stale.go", "old")
	writeFixture(t, source, "new.go", "new")
	writeFixture(t, parent, "sibling", "keep")
	if err := install(source, target); err != nil {
		t.Fatal(err)
	}
	if err := compare(source, target); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(parent, "sibling"))
	if err != nil || string(b) != "keep" {
		t.Fatalf("sibling changed: %q, %v", b, err)
	}
}

func TestDownloadRejectsWrongIdentity(t *testing.T) {
	p := pin{Path: upstreamPath, Version: "v1.3.0", Sum: "h1:expected"}
	if err := verifyDownload(&p, download{Path: upstreamPath, Version: "v1.3.1"}); err == nil {
		t.Fatal("wrong module version accepted")
	}
}

func writeFixture(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}
