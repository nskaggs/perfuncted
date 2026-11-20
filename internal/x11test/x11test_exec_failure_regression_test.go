package x11test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDisplayServerWithMissingInterpreterFailsWithoutPublishingDisplay(t *testing.T) {
	directory := t.TempDir()
	server := filepath.Join(directory, "Xvfb")
	interpreter := filepath.Join(directory, "missing-interpreter")
	if err := os.WriteFile(server, []byte("#!"+interpreter+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	display, stop, err := startServerWithTimeout("Xvfb", "", func() []string { return nil }, time.Second)
	if display != "" || stop != nil || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "x11test: start Xvfb") {
		t.Fatalf("unexecutable server returned display=%q stop=%v err=%v", display, stop != nil, err)
	}
}
