package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/spf13/cobra"
)

func TestDocumentationCLIWritesActualMarkdownAndRejectsInvalidDestination(t *testing.T) {
	root := &cobra.Command{Use: "pf"}
	root.AddCommand(docsCmd(root))
	target := filepath.Join(t.TempDir(), "generated")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"docs", "--dir", target})
	if err := root.Execute(); err != nil {
		t.Fatalf("generate documentation: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(target, "pf_docs.md"))
	if err != nil {
		t.Fatalf("documentation not emitted: %v", err)
	}
	if !strings.Contains(string(generated), "--dir") || !strings.Contains(out.String(), target) {
		t.Fatalf("missing documented option or destination: %q / %q", generated, out.String())
	}
	blocker := filepath.Join(t.TempDir(), "file-not-directory")
	if err := os.WriteFile(blocker, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"docs", "--dir", filepath.Join(blocker, "subdir")})
	if err := root.Execute(); err == nil {
		t.Fatal("documentation wrote beneath a regular file")
	}
	if got, err := os.ReadFile(blocker); err != nil || string(got) != "safe" {
		t.Fatalf("failed generation mutated parent: %q, %v", got, err)
	}
}

func TestDocumentationAndVersionWritersPropagateFailureAtEveryStage(t *testing.T) {
	root := &cobra.Command{Use: "pf"}
	root.AddCommand(docsCmd(root))
	target := filepath.Join(t.TempDir(), "docs")
	root.SetArgs([]string{"docs", "--dir", target})
	root.SetOut(scriptBrokenWriter{})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "synthetic output failure") {
		t.Fatalf("documentation report swallowed output failure: %v", err)
	}
	for stage := 1; stage <= 4; stage++ {
		version := versionCmd()
		output := &infoFailingWriter{max: stage}
		version.SetOut(output)
		if err := version.Execute(); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("version report stage %d: %v", stage, err)
		}
	}
}

func TestInfoAndSessionTypeCLIExposeSyntheticSessionWithoutDesktop(t *testing.T) {
	session := pftest.New(nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	opens := 0
	open := func(ctx context.Context) (*perfuncted.Session, error) { opens++; return session, nil }
	info := infoCmd(open)
	var out bytes.Buffer
	info.SetOut(&out)
	info.SetArgs([]string{"--output", "json"})
	if err := info.Execute(); err != nil {
		t.Fatalf("synthetic info JSON: %v", err)
	}
	if !strings.Contains(out.String(), "\"capabilities\"") || opens != 1 {
		t.Fatalf("info did not report synthetic capabilities: opens=%d out=%s", opens, out.String())
	}
	info = infoCmd(open)
	info.SetArgs([]string{"--output", "invalid"})
	if err := info.Execute(); err == nil || opens != 1 {
		t.Fatalf("invalid format opened session or succeeded: opens=%d err=%v", opens, err)
	}
	fail := errors.New("synthetic opener rejected environment")
	info = infoCmd(func(context.Context) (*perfuncted.Session, error) { return nil, fail })
	if err := info.Execute(); !errors.Is(err, fail) {
		t.Fatalf("opener failure swallowed: %v", err)
	}
	typeCommand := sessionTypeCmd()
	typeCommand.SetOut(&out)
	if err := typeCommand.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "session:") {
		t.Fatalf("session type did not render: %s", out.String())
	}
	for stage := 1; stage <= 4; stage++ {
		cmd := sessionTypeCmd()
		cmd.SetOut(&infoFailingWriter{max: stage})
		if err := cmd.Execute(); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("session type report stage %d swallowed error: %v", stage, err)
		}
	}
}
