package main

import (
	"os"
	"testing"
)

func shortPrivateSocketDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("/var/tmp", "pf-")
	if err != nil {
		t.Fatalf("create short socket directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove short socket directory: %v", err)
		}
	})
	return dir
}
