//go:build linux

package window

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

func TestKWinScriptStagingFailureRemovesPrivateFile(t *testing.T) {
	const childEnv = "PF_TEST_KWIN_FILE_SIZE_LIMIT"
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
		defer func() {
			if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
				t.Errorf("restore file size limit: %v", err)
			}
		}()
		path, err := writeKWinScript(func(string) string {
			return "callDBus('org.kde.private', '/', 'org.kde.private', 'ReportWindows', 'private');"
		}, "org.kde.private")
		if path != "" || !errors.Is(err, syscall.EFBIG) || !strings.Contains(err.Error(), "write temp file") {
			t.Fatalf("quota failure returned path %q, err %v", path, err)
		}
		entries, err := os.ReadDir(os.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("failed script staging retained private artifacts: %v", entries)
		}
		return
	}

	dir := t.TempDir()
	args := []string{"-test.run=^TestKWinScriptStagingFailureRemovesPrivateFile$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1", "TMPDIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated quota test failed: %v\n%s", err, output)
	}
}
