package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

func TestNewRootCmdConfiguresCobra(t *testing.T) {
	cmd := newRootCmd(func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
		return func() (*perfuncted.Perfuncted, error) { return nil, nil }
	})

	if got, want := cmd.Use, "pf"; got != want {
		t.Fatalf("Use = %q, want %q", got, want)
	}
	if !cmd.SilenceUsage {
		t.Fatal("SilenceUsage = false")
	}
	if !cmd.SilenceErrors {
		t.Fatal("SilenceErrors = false")
	}

	wantSubs := map[string]bool{
		"screen":    true,
		"input":     true,
		"window":    true,
		"find":      true,
		"clipboard": true,
		"info":      true,
		"session":   true,
		"docs":      true,
		"version":   true,
	}
	for _, sub := range cmd.Commands() {
		delete(wantSubs, sub.Name())
	}
	if len(wantSubs) != 0 {
		t.Fatalf("missing subcommands: %v", wantSubs)
	}
}

func TestRunWithFactoryCapturesStdout(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"screen", "resolution"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
		return func() (*perfuncted.Perfuncted, error) {
			sc := &pftest.Screenshotter{Width: 8, Height: 8}
			return pftest.New(sc, nil, nil, nil), nil
		}
	})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got, want := stdout, "8x8\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRunWithFactoryReportsParseError(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"screen", "grab", "--rect", "bad", "--out", "/tmp/pf-test.png"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
		return func() (*perfuncted.Perfuncted, error) {
			return pftest.New(&pftest.Screenshotter{Width: 8, Height: 8}, nil, nil, nil), nil
		}
	})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, `--rect must be x0,y0,x1,y1; got "bad"`) {
		t.Fatalf("stderr = %q, want rect parse error", stderr)
	}
}

func TestWindowCommands(t *testing.T) {
	t.Run("list plain", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{{
			{ID: 7, Title: "Firefox", AppID: "firefox", PID: 42, Active: true},
			{ID: 8, Title: "Terminal", AppID: "org.gnome.Terminal", PID: 99},
		}}}
		stdout, stderr, code := captureRunIO(t, []string{"window", "list", "--output", "plain"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "0x7\tFirefox") || !strings.Contains(stdout, "app_id=firefox") {
			t.Fatalf("stdout = %q, want plain window rows", stdout)
		}
	})

	t.Run("list json", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{{
			{ID: 7, Title: "Firefox", AppID: "firefox", PID: 42, Active: true},
		}}}
		stdout, stderr, code := captureRunIO(t, []string{"window", "list", "--output", "json"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		var got []window.Info
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("json.Unmarshal(stdout) error = %v; stdout=%q", err, stdout)
		}
		if len(got) != 1 || got[0].Title != "Firefox" || got[0].ID != 7 {
			t.Fatalf("decoded windows = %+v, want Firefox ID 7", got)
		}
	})

	t.Run("find", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{{
			{ID: 7, Title: "Firefox", AppID: "firefox", PID: 42},
			{ID: 8, Title: "Firefox Settings", AppID: "firefox", PID: 43},
		}}}
		stdout, stderr, code := captureRunIO(t, []string{"window", "find", "app_id:firefox", "state:-minimized"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "Firefox") || !strings.Contains(stdout, "Firefox Settings") {
			t.Fatalf("stdout = %q, want matching windows", stdout)
		}
	})

	t.Run("wait-for", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{
			{},
			{{ID: 11, Title: "Firefox", AppID: "firefox", PID: 42}},
		}}
		stdout, stderr, code := captureRunIO(t, []string{"window", "wait-for", "title:Firefox", "--poll", "1ms", "--timeout", "50ms"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "Firefox") {
			t.Fatalf("stdout = %q, want matching window", stdout)
		}
	})

	t.Run("wait-close", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{
			{{ID: 11, Title: "Firefox", AppID: "firefox", PID: 42}},
			{},
		}}
		stdout, stderr, code := captureRunIO(t, []string{"window", "wait-close", "title:Firefox", "--poll", "1ms", "--timeout", "50ms"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
	})
}

func captureRunIO(t *testing.T, args []string, openPFFactory func(*cliConfig) func() (*perfuncted.Perfuncted, error)) (string, string, int) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr

	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout = wOut
	os.Stderr = wErr
	code := runWithFactory(context.Background(), args, openPFFactory)

	wOut.Close()
	wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	outBytes, err := io.ReadAll(rOut)
	if err != nil {
		t.Fatal(err)
	}
	errBytes, err := io.ReadAll(rErr)
	if err != nil {
		t.Fatal(err)
	}

	return string(outBytes), string(errBytes), code
}

func TestVersionCmd(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"version"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
		return func() (*perfuncted.Perfuncted, error) {
			return nil, nil
		}
	})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "pf dev") {
		t.Fatalf("stdout = %q, want it to contain 'pf dev'", stdout)
	}
}
