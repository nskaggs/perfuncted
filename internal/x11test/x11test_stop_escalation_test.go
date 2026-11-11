package x11test

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A display server may ignore SIGTERM. Cleanup must escalate to SIGKILL
// against its process group and reap the original child.
func TestStopDisplayEscalatesForTermIgnoringServer(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("sh", "-c", "trap '' TERM; : > \"$1\"; while :; do sleep 0.2; done", "sh", ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server failed to initialize")
		}
		time.Sleep(10 * time.Millisecond)
	}
	started := time.Now()
	stopDisplay(cmd)
	if cmd.ProcessState == nil {
		t.Fatal("server was not reaped")
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("expected SIGKILL escalation, got %v", cmd.ProcessState)
	}
	elapsed := time.Since(started)
	if elapsed < 1800*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("unexpected termination escalation latency: %s", elapsed)
	}
}

func TestStopDisplayToleratesNoProcess(t *testing.T) {
	stopDisplay(nil)
	stopDisplay(&exec.Cmd{})
}
