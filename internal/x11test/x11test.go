package x11test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nskaggs/perfuncted/internal/executil"
)

const (
	startupTimeout = 15 * time.Second
)

// synchronizedBuffer keeps diagnostic reads safe while os/exec drains stderr.
type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

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
	return startServerWithTimeout(name, hostDisplay, argsFn, startupTimeout)
}

//nolint:gocyclo // This protocol boundary explicitly handles distinct safety and error cases.
func startServerWithTimeout(name, hostDisplay string, argsFn func() []string, timeout time.Duration) (display string, stop func(), err error) {
	if _, lookErr := executil.LookPath(name); lookErr != nil {
		return "", nil, fmt.Errorf("x11test: %s not found: %w", name, lookErr)
	}

	var stderr synchronizedBuffer
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
		// The server must send one complete line containing only the
		// allocated display number. Limit the read so an untrusted child
		// cannot grow memory indefinitely before publishing its identity.
		line, readErr := bufio.NewReader(io.LimitReader(pr, 64)).ReadString('\n')
		var num int
		if readErr == nil {
			response := strings.TrimSuffix(line, "\n")
			if response == "" {
				readErr = fmt.Errorf("empty display identity")
			} else {
				for _, char := range response {
					if char < '0' || char > '9' {
						readErr = fmt.Errorf("invalid display identity %q", response)
						break
					}
				}
			}
			if readErr == nil {
				num, readErr = strconv.Atoi(response)
				if readErr == nil && num > 65535 {
					readErr = fmt.Errorf("display identity out of range")
				}
			}
		}
		displayCh <- struct {
			num int
			err error
		}{num: num, err: readErr}
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
	case <-time.After(timeout):
		stopDisplay(cmd)
		return "", nil, fmt.Errorf("x11test: %s did not become ready within %s; stderr: %s", name, timeout, strings.TrimSpace(stderr.String()))
	}
}

func stopDisplay(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		// exec.Cmd.Wait also joins the goroutine draining stderr into the
		// synchronized diagnostic buffer; Process.Wait alone races it.
		_ = cmd.Wait()
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
