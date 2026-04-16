//go:build !windows
// +build !windows

package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWaitForFile_Succeeds(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "f.txt")
	// create file after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(p, []byte("x"), 0644)
	}()
	if err := waitForFile(p, 20, 10*time.Millisecond); err != nil {
		t.Fatalf("waitForFile failed: %v", err)
	}
}

func TestWaitForFile_TimesOut(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notexists")
	if err := waitForFile(p, 2, 10*time.Millisecond); err == nil {
		t.Fatalf("waitForFile succeeded unexpectedly")
	}
}

func TestWaitForGlob_Succeeds(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "match.sock")
	pattern := filepath.Join(d, "*.sock")
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(p, []byte("x"), 0644)
	}()
	if err := waitForGlob(pattern, 20, 10*time.Millisecond); err != nil {
		t.Fatalf("waitForGlob failed: %v", err)
	}
}

func TestWaitForGlob_TimesOut(t *testing.T) {
	pattern := filepath.Join(t.TempDir(), "*.nope")
	if err := waitForGlob(pattern, 2, 10*time.Millisecond); err == nil {
		t.Fatalf("waitForGlob succeeded unexpectedly")
	}
}

func TestManagedProcStop_KillsProcess(t *testing.T) {
	// start a sleep process that would outlive the timeout
	cmd := exec.Command("sh", "-c", "sleep 10")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	m := &managedProc{cmd: cmd, pid: pid}
	m.stop(200 * time.Millisecond)
	// check process does not exist
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("process %d still exists", pid)
	} else if err != syscall.ESRCH {
		// other errors are unusual but not fatal for this test
		t.Logf("kill check returned err: %v", err)
	}
}
