//go:build linux

package input

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestPortalTokenWriteFailurePreservesExistingGrant(t *testing.T) {
	const childEnv = "PF_TEST_PORTAL_TOKEN_FILE_SIZE_LIMIT"
	if os.Getenv(childEnv) == "1" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		secureRoot, err := os.MkdirTemp(home, ".perfuncted-token-test-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(secureRoot)
		state := filepath.Join(secureRoot, "state")
		lock, err := lockPortalTokenStore(env.FromEnviron([]string{"XDG_STATE_HOME=" + state}))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := lock.Close(); err != nil {
				t.Error(err)
			}
		}()
		if err := lock.store("existing-grant"); err != nil {
			t.Fatal(err)
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
		storeErr := lock.store("new-grant")
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(storeErr, syscall.EFBIG) {
			t.Fatalf("failed token write returned %v, want EFBIG", storeErr)
		}
		entries, err := os.ReadDir(filepath.Join(state, "perfuncted"))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".restore-token-") {
				t.Fatalf("failed rotation left temporary grant %q", entry.Name())
			}
		}
		token, err := lock.consume()
		if err != nil || token != "existing-grant" {
			t.Fatalf("existing grant after failed rotation = %q, %v", token, err)
		}
		if err := lock.store("recovered-grant"); err != nil {
			t.Fatalf("store did not recover: %v", err)
		}
		return
	}

	dir := t.TempDir()
	args := []string{"-test.run=^TestPortalTokenWriteFailurePreservesExistingGrant$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1", "TMPDIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated quota test failed: %v\n%s", err, output)
	}
}
