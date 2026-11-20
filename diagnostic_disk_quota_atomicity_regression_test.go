//go:build linux

package perfuncted

import (
	"errors"
	"flag"
	"image"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

func TestDiagnosticDiskQuotaFailurePreservesArtifacts(t *testing.T) {
	const childEnv = "PF_TEST_DIAGNOSTIC_FILE_SIZE_LIMIT"
	if os.Getenv(childEnv) == "1" {
		dir := os.TempDir()
		manifest := filepath.Join(dir, "manifest.json")
		screenshot := filepath.Join(dir, "screen.png")
		for _, path := range []string{manifest, screenshot} {
			if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		signal.Ignore(syscall.SIGXFSZ)
		var original syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Fatal(err)
		}
		limited := original
		limited.Cur = 0
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
			t.Fatal(err)
		}
		atomicErr := writeAtomic(manifest, []byte("replacement"), 0o600)
		pngErr := writePNG(screenshot, image.NewRGBA(image.Rect(0, 0, 2, 2)))
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(atomicErr, syscall.EFBIG) || !errors.Is(pngErr, syscall.EFBIG) {
			t.Fatalf("quota failures: manifest=%v screenshot=%v", atomicErr, pngErr)
		}
		for _, path := range []string{manifest, screenshot} {
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "existing" {
				t.Fatalf("failed artifact write changed %s: %q, %v", filepath.Base(path), data, err)
			}
		}
		assertNoDiagnosticTemporaryArtifacts(t, dir)
		return
	}

	dir := t.TempDir()
	args := []string{"-test.run=^TestDiagnosticDiskQuotaFailurePreservesArtifacts$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1", "TMPDIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated quota test failed: %v\n%s", err, output)
	}
}
