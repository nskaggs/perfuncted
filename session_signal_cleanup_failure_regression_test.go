package perfuncted

import (
	"bytes"
	"context"
	"flag"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type signalCleanupLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	notify chan struct{}
}

func (l *signalCleanupLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	n, err := l.buffer.Write(p)
	l.mu.Unlock()
	select {
	case l.notify <- struct{}{}:
	default:
	}
	return n, err
}

func (l *signalCleanupLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.String()
}

// The signal handler must report teardown failure and retain a runtime whose
// permissions were changed, rather than deleting potentially unowned data.
func TestSessionSignalCleanupRetainsUnsafeRuntimeDirectory(t *testing.T) {
	const childEnv = "PF_TEST_SIGNAL_CLEANUP_FAILURE"
	const dirEnv = "PF_TEST_SIGNAL_CLEANUP_FAILURE_DIR"
	if os.Getenv(childEnv) != "1" {
		dir := filepath.Join(shortPrivateRuntimeDir(t), "unsafe-runtime")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		args := []string{"-test.run=^TestSessionSignalCleanupRetainsUnsafeRuntimeDirectory$", "-test.timeout=8s"}
		if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
			args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], args...)
		cmd.Env = append(os.Environ(), childEnv+"=1", dirEnv+"="+dir)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated signal cleanup: %v\n%s", err, output)
		}
		return
	}

	dir := os.Getenv(dirEnv)
	if dir == "" {
		t.Fatal("missing isolated runtime path")
	}
	marker := dir + "/preserve"
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: dir, stopDone: make(chan struct{})}
	logs := &signalCleanupLog{notify: make(chan struct{}, 1)}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	defer slog.SetDefault(previous)
	stop := infra.CleanupOnSignal(context.Background())
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-infra.stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("signal did not finish automatic cleanup")
	}
	if infra.stopErr == nil || !strings.Contains(infra.stopErr.Error(), "refuse unsafe managed runtime") {
		t.Fatalf("unsafe cleanup error = %v", infra.stopErr)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "keep" {
		t.Fatalf("unsafe runtime contents were changed: data=%q err=%v", data, err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		text := logs.text()
		if strings.Contains(text, "skip removal of runtime") && strings.Contains(text, "signal-triggered cleanup failed") {
			break
		}
		select {
		case <-logs.notify:
		case <-deadline.C:
			t.Fatalf("missing cleanup diagnostics: %s", logs.text())
		}
	}
}
