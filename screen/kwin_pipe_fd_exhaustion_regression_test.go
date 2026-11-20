//go:build linux

package screen

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestKWinCaptureFailsClosedWhenPipeDescriptorsExhausted(t *testing.T) {
	const childEnv = "PF_TEST_KWIN_DESCRIPTOR_LIMIT"
	if os.Getenv(childEnv) == "1" {
		var original syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &original); err != nil {
			t.Fatal(err)
		}
		limited := original
		if limited.Cur > 64 {
			limited.Cur = 64
		}
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limited); err != nil {
			t.Fatal(err)
		}
		var files []*os.File
		for {
			f, err := os.Open("/dev/null")
			if errors.Is(err, syscall.EMFILE) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		img, captureErr := (&kwinDBusTransport{}).CaptureActiveScreen(context.Background())
		for _, f := range files {
			if err := f.Close(); err != nil {
				t.Errorf("release test descriptor: %v", err)
			}
		}
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &original); err != nil {
			t.Fatal(err)
		}
		if img != nil || !errors.Is(captureErr, syscall.EMFILE) || !strings.Contains(captureErr.Error(), "pipe") {
			t.Fatalf("descriptor exhaustion returned image=%v err=%v", img, captureErr)
		}
		return
	}

	args := []string{"-test.run=^TestKWinCaptureFailsClosedWhenPipeDescriptorsExhausted$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated descriptor test failed: %v\n%s", err, output)
	}
}
