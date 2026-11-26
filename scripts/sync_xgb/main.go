// Command sync_xgb reproduces the pinned, locally patched XGB source tree.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	"golang.org/x/mod/zip"
)

const upstreamPath = "github.com/jezek/xgb"
const internalPath = "github.com/nskaggs/perfuncted/v2/internal/third_party/xgb"

type pin struct {
	Path    string   `json:"path"`
	Version string   `json:"version"`
	Commit  string   `json:"commit"`
	Sum     string   `json:"sum"`
	ModSum  string   `json:"goModSum"`
	Patches []string `json:"patches"`
}

type download struct {
	Path, Version, Sum, GoModSum, Zip string
	Origin                            *struct{ Hash string }
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if !validArgs(args) {
		return errors.New("usage: sync_xgb check | sync | update vX.Y.Z (preview only)")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	spec := filepath.Join(root, "third_party", "xgb")
	target := filepath.Join(root, "internal", "third_party", "xgb")
	p, err := readPin(filepath.Join(spec, "upstream.json"))
	if err != nil {
		return err
	}
	if args[0] == "update" {
		if module.CanonicalVersion(args[1]) != args[1] || args[1] == "" {
			return errors.New("update requires an exact canonical Go module version")
		}
		p.Version, p.Sum, p.ModSum, p.Commit = args[1], "", "", ""
	}
	scratch, err := os.MkdirTemp("", "perfuncted-xgb-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	tree := filepath.Join(scratch, "source")
	if err := generate(root, spec, tree, &p); err != nil {
		return err
	}
	return finish(args[0], tree, target, p)
}

func validArgs(args []string) bool {
	if len(args) == 1 {
		return args[0] == "check" || args[0] == "sync"
	}
	return len(args) == 2 && args[0] == "update"
}

func finish(action, tree, target string, p pin) error {
	switch action {
	case "check":
		if err := compare(tree, target); err != nil {
			return fmt.Errorf("generated XGB differs: %w; review patches and run just xgb-sync", err)
		}
		fmt.Printf("XGB %s: exact source reproduction verified\n", p.Version)
	case "sync":
		if err := compare(tree, target); err == nil {
			fmt.Println("XGB already matches pinned source")
			return nil
		}
		if err := install(tree, target); err != nil {
			return err
		}
		fmt.Println("XGB regenerated; review the Git diff and run tests")
	case "update":
		// Preview never edits the manifest, patches, or checked-in source.
		encoded, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return err
		}
		fmt.Printf("Candidate patches applied cleanly; review and test before adoption.\n%s\n", encoded)
		if err := compare(tree, target); err != nil {
			fmt.Printf("Candidate differs from current source: %v\n", err)
		}
	}
	return nil
}

func readPin(name string) (pin, error) {
	var p pin
	data, err := os.ReadFile(name)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, err
	}
	if p.Path != upstreamPath || p.Version == "" || module.CanonicalVersion(p.Version) != p.Version ||
		!strings.HasPrefix(p.Sum, "h1:") || !strings.HasPrefix(p.ModSum, "h1:") || p.Commit == "" || len(p.Patches) == 0 {
		return p, errors.New("invalid XGB pin: path, exact version, commit, module checksums and ordered patches required")
	}
	seen := map[string]bool{}
	for _, patch := range p.Patches {
		if !filepath.IsLocal(patch) || filepath.Base(patch) != patch || !strings.HasSuffix(patch, ".patch") || seen[patch] {
			return p, fmt.Errorf("invalid or duplicate patch %q", patch)
		}
		seen[patch] = true
	}
	return p, nil
}

func generate(root, spec, tree string, p *pin) error {
	cmd := exec.Command("go", "mod", "download", "-json", p.Path+"@"+p.Version)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	data, downloadErr := cmd.Output()
	if downloadErr != nil {
		return fmt.Errorf("download XGB %s: %w: %s", p.Version, downloadErr, data)
	}
	var d download
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	if err := verifyDownload(p, d); err != nil {
		return err
	}
	if err := zip.Unzip(tree, module.Version{Path: p.Path, Version: p.Version}, d.Zip); err != nil {
		return fmt.Errorf("extract XGB archive: %w", err)
	}
	if err := normalize(tree); err != nil {
		return err
	}
	license, err := os.ReadFile(filepath.Join(tree, "LICENSE"))
	if err != nil {
		return err
	}
	for _, patch := range p.Patches {
		if patchErr := applyPatch(tree, filepath.Join(spec, "patches", patch)); patchErr != nil {
			return fmt.Errorf("XGB %s patch %s: %w", p.Version, patch, patchErr)
		}
	}
	after, err := os.ReadFile(filepath.Join(tree, "LICENSE"))
	if err != nil || !bytes.Equal(license, after) {
		return errors.New("patch series must preserve the upstream LICENSE exactly")
	}
	return nil
}

func verifyDownload(p *pin, d download) error {
	if d.Path != p.Path || d.Version != p.Version || d.Sum == "" || d.GoModSum == "" || d.Zip == "" {
		return errors.New("download identity or checksum missing")
	}
	actual, err := dirhash.HashZip(d.Zip, dirhash.Hash1)
	if err != nil {
		return err
	}
	if actual != d.Sum || (p.Sum != "" && (p.Sum != d.Sum || p.ModSum != d.GoModSum)) {
		return errors.New("XGB archive does not match pinned Go module checksums")
	}
	if err := verifyOrigin(p, d); err != nil {
		return err
	}
	p.Sum, p.ModSum = d.Sum, d.GoModSum
	return nil
}

func verifyOrigin(p *pin, d download) error {
	if d.Origin != nil && d.Origin.Hash != "" {
		if p.Commit != "" && p.Commit != d.Origin.Hash {
			return errors.New("XGB upstream commit mismatch")
		}
		p.Commit = d.Origin.Hash
	}
	if p.Commit == "" {
		return errors.New("candidate commit provenance unavailable; obtain verified module origin before adoption")
	}
	return nil
}

func normalize(tree string) error {
	if err := os.Remove(filepath.Join(tree, "go.mod")); err != nil {
		return err
	}
	return filepath.WalkDir(tree, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected upstream file type: %s", path)
		}
		// The Go module archive is immutable; the extracted scratch copy is
		// writable so patches can be applied. The published tree is not a module.
		if chmodErr := os.Chmod(path, 0644); chmodErr != nil {
			return chmodErr
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, bytes.ReplaceAll(b, []byte(upstreamPath), []byte(internalPath)), 0644)
	})
}

func applyPatch(tree, name string) error {
	for _, check := range []bool{true, false} {
		args := []string{"apply", "-p2", "--whitespace=nowarn"}
		if check {
			args = append(args, "--check")
		}
		cmd := exec.Command("git", append(args, "--", name)...)
		cmd.Dir = tree
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%w: %s", err, output)
		}
	}
	return nil
}

func compare(want, got string) error {
	expected, err := files(want)
	if err != nil {
		return err
	}
	actual, err := files(got)
	if err != nil {
		return err
	}
	if !slices.Equal(expected, actual) {
		return errors.New("file inventory mismatch (missing or extra file)")
	}
	for _, name := range expected {
		a, err := os.ReadFile(filepath.Join(want, name))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(got, name))
		if err != nil {
			return err
		}
		if !bytes.Equal(a, b) {
			return fmt.Errorf("content mismatch: %s", name)
		}
	}
	return nil
}

func files(root string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected file type: %s", path)
		}
		name, err := filepath.Rel(root, path)
		if err == nil {
			names = append(names, name)
		}
		return err
	})
	return names, err
}

func install(tree, target string) error {
	// Stage beside the target so renames are on the same filesystem.
	stage, err := os.MkdirTemp(filepath.Dir(target), ".xgb-sync-")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stage)
		}
	}()
	next := filepath.Join(stage, "next")
	if err := os.CopyFS(next, os.DirFS(tree)); err != nil {
		return err
	}
	if err := compare(tree, next); err != nil {
		return err
	}
	old := filepath.Join(stage, "old")
	if err := os.Rename(target, old); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return os.Rename(next, target)
		}
		return err
	}
	if err := os.Rename(next, target); err != nil {
		if restoreErr := os.Rename(old, target); restoreErr != nil {
			cleanup = false
			return fmt.Errorf("install failed: %w; restore failed: %w; source preserved at %s", err, restoreErr, old)
		}
		return err
	}
	return nil
}
