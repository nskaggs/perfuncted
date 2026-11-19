package window

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Temporary KWin scripts carry an authenticated callback bus name. Refuse
// creation when private storage is unavailable instead of running the builder
// or publishing a script at an unintended location.
func TestKWinScriptTransportRejectsUnusableTemporaryDirectory(t *testing.T) {
	root := t.TempDir()
	notDirectory := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(notDirectory, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", notDirectory)
	called := false
	path, err := writeKWinScript(func(string) string {
		called = true
		return "callDBus('untrusted');"
	}, "org.example.private")
	if err == nil || !strings.Contains(err.Error(), "temp file") || path != "" {
		t.Fatalf("unusable temp storage returned path %q, err %v", path, err)
	}
	if called {
		t.Fatal("script builder ran without private temporary storage")
	}
	data, err := os.ReadFile(notDirectory)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("unusable storage altered: contents %q, error %v", data, err)
	}
}

// The written file must contain only the generated JS and remain private; it
// is later loaded by the privileged KWin scripting D-Bus interface.
func TestKWinScriptTransportWritesPrivateExactContent(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const service = "org.kde.pflist123_456"
	want := "callDBus('" + service + "', '/', '" + service + "', 'ReportWindows', 'a\\\\b');"
	path, err := writeKWinScript(func(got string) string {
		if got != service {
			t.Fatalf("builder service = %q, want %q", got, service)
		}
		return want
	}, service)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("script permissions = %o, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("script bytes = %q err=%v, want %q", data, err, want)
	}
}
