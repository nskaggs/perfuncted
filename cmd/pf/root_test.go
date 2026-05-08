package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
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
