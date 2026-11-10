//go:build integration && linux

package flatpaktest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Catch drift between go.mod, vendor/modules.txt, and the exact Flatpak archives.
func TestFlatpakVendorMatchesModuleGraph(t *testing.T) {
	repo := mustRepoRoot(t)
	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	mod := read("go.mod")
	manifest := read("io.github.nskaggs.perfuncted.yml")
	vendor := read("flatpak/modules.txt")
	require := regexp.MustCompile(`(?m)^\s+([A-Za-z0-9._/-]+)\s+(v[0-9][^\s]+)`)
	deps := map[string]string{}
	for _, match := range require.FindAllStringSubmatch(mod, -1) {
		name, version := match[1], match[2]
		if prior, ok := deps[name]; ok && prior != version {
			t.Fatalf("duplicate %s %s %s", name, prior, version)
		}
		deps[name] = version
	}
	if len(deps) < 10 {
		t.Fatalf("too few dependencies: %d", len(deps))
	}
	matches := regexp.MustCompile(`(?m)^# ([A-Za-z0-9._/-]+) (v[0-9][^\s]+)$`)
	for _, match := range matches.FindAllStringSubmatch(vendor, -1) {
		name, version := match[1], match[2]
		if deps[name] != version {
			t.Errorf("vendor mismatch: %s at %s but go.mod requires %s", name, version, deps[name])
		}
	}
	for name, version := range deps {
		if !strings.Contains(vendor, "# "+name+" "+version+"\n") {
			t.Errorf("vendor missing %s@%s", name, version)
		}
		if !strings.Contains(manifest, "https://proxy.golang.org/"+name+"/@v/"+version+".zip") {
			t.Errorf("Flatpak missing source %s@%s", name, version)
		}
	}
}
