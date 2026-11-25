//go:build linux

package runtime

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A relative log root cannot be resolved safely after its working directory
// disappears. Retention must fail closed rather than looking elsewhere.
func TestSessionLogRetentionRejectsRelativeRootAfterWorkingDirectoryRemoval(t *testing.T) {
	const childEnv = "PF_TEST_DELETED_LOG_CWD"
	if os.Getenv(childEnv) == "1" {
		cwd := os.Getenv("PF_TEST_DELETED_LOG_CWD_PATH")
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(cwd); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Getwd(); err == nil {
			t.Fatal("deleted cwd unexpectedly resolved")
		}
		if sessionLogRootHasLiteralDirectories("relative-session-logs") {
			t.Fatal("relative log root beneath deleted cwd was accepted")
		}
		CleanupSessionLogs("relative-session-logs", time.Hour)
		if _, err := os.Stat(filepath.Join(filepath.Dir(cwd), "relative-session-logs")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cleanup created or touched fallback root: %v", err)
		}
		return
	}
	parent := t.TempDir()
	cwd := filepath.Join(parent, "deleted-cwd")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestSessionLogRetentionRejectsRelativeRootAfterWorkingDirectoryRemoval$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1", "PF_TEST_DELETED_LOG_CWD_PATH="+cwd)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated deleted-cwd retention test failed: %v\n%s", err, output)
	}
}
