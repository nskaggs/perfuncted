package x11test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/nskaggs/perfuncted/internal/executil"
)

const (
	startupTimeout = 15 * time.Second
)

// StartXvfb starts a standalone Xvfb server on a free display.
func StartXvfb() (display string, stop func(), err error) {
	return startServer("Xvfb", "", func() []string {
		return []string{"-screen", "0", "1024x768x24", "-ac"}
	})
}

// StartXephyr starts a nested Xephyr server on a free display using hostDisplay.
func StartXephyr(hostDisplay string) (display string, stop func(), err error) {
	if hostDisplay == "" {
		return "", nil, fmt.Errorf("x11test: host DISPLAY not set")
	}
	return startServer("Xephyr", hostDisplay, func() []string {
		return []string{"-screen", "1024x768", "-ac", "-br", "-reset"}
	})
}

func startServer(name, hostDisplay string, argsFn func() []string) (display string, stop func(), err error) {
	if _, err := executil.LookPath(name); err != nil {
		return "", nil, fmt.Errorf("x11test: %s not found: %w", name, err)
	}

	var stderr bytes.Buffer
	pr, pw, err := os.Pipe()
	if err != nil {
		return "", nil, fmt.Errorf("x11test: create displayfd pipe: %w", err)
	}
	defer pr.Close()

	cmd := exec.Command(name, append(argsFn(), "-displayfd", "3")...)
	if hostDisplay != "" {
		cmd.Env = append(os.Environ(), "DISPLAY="+hostDisplay)
	}
	cmd.ExtraFiles = []*os.File{pw}
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return "", nil, fmt.Errorf("x11test: start %s: %w", name, err)
	}
	_ = pw.Close()

	displayCh := make(chan struct {
		num int
		err error
	}, 1)
	go func() {
		var num int
		_, err := fmt.Fscan(pr, &num)
		displayCh <- struct {
			num int
			err error
		}{num: num, err: err}
	}()

	select {
	case res := <-displayCh:
		if res.err != nil {
			stopDisplay(cmd)
			return "", nil, fmt.Errorf("x11test: %s did not report a display: %w; stderr: %s", name, res.err, strings.TrimSpace(stderr.String()))
		}
		display = fmt.Sprintf(":%d", res.num)
		stop = func() {
			stopDisplay(cmd)
		}
		return display, stop, nil
	case <-time.After(startupTimeout):
		stopDisplay(cmd)
		return "", nil, fmt.Errorf("x11test: %s did not become ready within %s; stderr: %s", name, startupTimeout, strings.TrimSpace(stderr.String()))
	}
}

func stopDisplay(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}
