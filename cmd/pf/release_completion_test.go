package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The real completion command must produce usable shell definitions, not
// merely appear in the command tree. Where bash is installed, syntax-check it.
func TestReleaseShellCompletionContracts(t *testing.T) {
	for _, tc := range []struct{ shell, marker string }{
		{"bash", "__start_pf"},
		{"zsh", "#compdef pf"},
		{"fish", "complete -c pf"},
		{"powershell", "Register-ArgumentCompleter"},
	} {
		t.Run(tc.shell, func(t *testing.T) {
			root := newRootCmd(defaultOpenPFFactory)
			var out, diagnostic bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&diagnostic)
			root.SetArgs([]string{"completion", tc.shell})
			if err := root.Execute(); err != nil || diagnostic.Len() != 0 || !strings.Contains(out.String(), tc.marker) || out.Len() < 100 {
				t.Fatalf("completion %s: %v stderr=%q output=%q", tc.shell, err, diagnostic.String(), out.String())
			}
			if tc.shell == "bash" {
				bash, err := exec.LookPath("bash")
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "pf.bash")
				if err := os.WriteFile(path, out.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(bash, "-n", path)
				if data, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("invalid bash completion: %v: %s", err, data)
				}
			}
		})
	}
}
