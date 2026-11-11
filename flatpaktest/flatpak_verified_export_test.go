//go:build integration && linux

package flatpaktest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFlatpakExportVerifiedBundleIsAtomicAndExplicit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "tested.flatpak")
	destination := filepath.Join(root, "published", "perfuncted.flatpak")
	payload := []byte("verified-flatpak-bundle-payload")
	if err := os.WriteFile(source, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := exportAcceptedFlatpakBundle(source, destination); err != nil {
		t.Fatalf("export known-good bundle: %v", err)
	}
	out, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(out, payload) {
		t.Fatalf("export corrupted verified bytes: %q, %v", out, err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("published artifact mode=%v, err=%v; want 0644", info, err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	out, err = os.ReadFile(destination)
	if err != nil || !bytes.Equal(out, payload) {
		t.Fatalf("published bundle depended on temporary source after cleanup: %v", err)
	}
	for _, tc := range []struct {
		name, file, target string
	}{
		{"missing source", source, destination},
		{"relative destination", destination, "relative.flatpak"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := exportAcceptedFlatpakBundle(tc.file, tc.target); err == nil {
				t.Fatal("unverified artifact export unexpectedly succeeded")
			}
			data, err := os.ReadFile(destination)
			if err != nil || !bytes.Equal(data, payload) {
				t.Fatalf("failed export damaged prior complete bundle: %v", err)
			}
		})
	}
	if err := os.WriteFile(source, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := exportAcceptedFlatpakBundle(source, destination); err == nil {
		t.Fatal("empty bundle was exported")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(destination, source); err != nil {
		t.Fatal(err)
	}
	if err := exportAcceptedFlatpakBundle(source, destination); err == nil {
		t.Fatal("symlink source should not be published as tested bytes")
	}
	out, err = os.ReadFile(destination)
	if err != nil || !bytes.Equal(out, payload) {
		t.Fatalf("failed export damaged the published artifact: %v", err)
	}
}
