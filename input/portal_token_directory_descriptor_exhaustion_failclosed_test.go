//go:build linux

package input

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// When the process has no available descriptors, the token-store directory
// must be rejected at its trusted root rather than using ambient paths or
// accepting an unverified restore token. The limit is process-global, so it
// is only changed in an isolated test subprocess.
func TestPortalTokenDirectoryRejectsExhaustedDescriptorTable(t *testing.T) {
	const child = "PF_TEST_PORTAL_TOKEN_DIRECTORY_FD_EXHAUSTED"
	if os.Getenv(child) == "1" {
		root := os.Getenv("PF_TEST_PORTAL_TOKEN_FD_ROOT")
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		var original unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &original); err != nil {
			t.Fatal(err)
		}
		limited := original
		limited.Cur = 0
		if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &limited); err != nil {
			t.Fatal(err)
		}
		fd, openErr := openPrivatePortalDirectory(root)
		restoreErr := unix.Setrlimit(unix.RLIMIT_NOFILE, &original)
		if restoreErr != nil {
			t.Fatal(restoreErr)
		}
		if fd >= 0 {
			_ = unix.Close(fd)
			t.Fatal("descriptor-exhausted token directory unexpectedly opened")
		}
		if !errors.Is(openErr, unix.EMFILE) {
			t.Fatalf("descriptor exhaustion returned %v; want EMFILE", openErr)
		}
		if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
			t.Fatalf("descriptor failure created token-store artifacts: %v, %v", entries, err)
		}
		return
	}
	root := filepath.Join(t.TempDir(), "private-state")
	args := []string{"-test.run=^TestPortalTokenDirectoryRejectsExhaustedDescriptorTable$"}
	if coverage := flag.Lookup("test.gocoverdir"); coverage != nil && coverage.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverage.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), child+"=1", "PF_TEST_PORTAL_TOKEN_FD_ROOT="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated descriptor exhaustion test failed: %v\n%s", err, output)
	}
}
