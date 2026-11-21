package main

import (
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Exercise the actual entrypoint in a child process: main uses os.Exit,
// so invoking it in the parent test would terminate the entire test runner.
// Invalid CLI requests must produce a failure exit without opening a session.
func TestCLIMainRejectsUnknownCommandWithFailureExit(t *testing.T) {
	const childEnv = "PF_TEST_CLI_MAIN_EXIT"
	if os.Getenv(childEnv) == "1" {
		os.Args = []string{"pf", "no-such-perfuncted-command"}
		main()
		t.Fatal("CLI entrypoint returned instead of exiting")
	}
	args := []string{"-test.run=^TestCLIMainRejectsUnknownCommandWithFailureExit$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	output, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("unknown CLI command exit error = %v, output = %q; want exit 1", err, output)
	}
	if !strings.Contains(string(output), "unknown command") || !strings.Contains(string(output), "no-such-perfuncted-command") {
		t.Fatalf("unknown command failure omitted diagnostics: %q", output)
	}
}
