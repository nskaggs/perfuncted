//go:build linux

package input

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// A virtual keyboard must not acknowledge or cache a keymap if the process
// cannot allocate its private shared-memory descriptor. Use an isolated
// subprocess because the descriptor limit is process-wide.
func TestVirtualKeyboardRejectsKeymapWhenDescriptorQuotaExhausted(t *testing.T) {
	const child = "PF_TEST_KEYBOARD_NO_FDS"
	if os.Getenv(child) == "1" {
		k, transport := newTestKeyboard()
		const keymap = "xkb_keymap { xkb_keycodes { <K008> = 8; }; };"
		var original syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &original); err != nil {
			t.Fatal(err)
		}
		limited := original
		limited.Cur = 0
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limited); err != nil {
			t.Fatal(err)
		}
		err := k.uploadKeymap(context.Background(), keymap)
		if restoreErr := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &original); restoreErr != nil {
			t.Fatal(restoreErr)
		}
		if err == nil || !strings.Contains(err.Error(), "keyboard shm") {
			t.Fatalf("exhausted descriptor quota accepted keymap: %v", err)
		}
		if k.lastKeymap != "" || transport.writes != 0 {
			t.Fatalf("failed allocation changed keyboard state: cache=%q writes=%d", k.lastKeymap, transport.writes)
		}
		return
	}
	args := []string{"-test.run=^TestVirtualKeyboardRejectsKeymapWhenDescriptorQuotaExhausted$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), child+"=1", "XDG_RUNTIME_DIR=")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated descriptor exhaustion failed: %v\n%s", err, output)
	}
}
