//go:build linux

package clipboard

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
)

func TestWaylandInputWriteFailureRemovesPrivateStaging(t *testing.T) {
	const childEnv = "PF_TEST_CLIPBOARD_FILE_SIZE_LIMIT"
	if os.Getenv(childEnv) == "1" {
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
		file, path, writeErr := waylandInput("private clipboard content")
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Fatal(err)
		}
		if file != nil || path != "" || !errors.Is(writeErr, syscall.EFBIG) || !strings.Contains(writeErr.Error(), "write input") {
			t.Fatalf("failed staging returned file=%v path=%q err=%v", file, path, writeErr)
		}
		entries, err := os.ReadDir(os.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("failed staging retained private artifacts: %v", entries)
		}
		return
	}

	dir := t.TempDir()
	args := []string{"-test.run=^TestWaylandInputWriteFailureRemovesPrivateStaging$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1", "TMPDIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated quota test failed: %v\n%s", err, output)
	}
}
