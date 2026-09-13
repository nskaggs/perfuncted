package wl

import "testing"

func TestWithOperationRunsCallback(t *testing.T) {
	ran := false
	if err := WithOperation(&Context{}, func() error {
		ran = true
		return nil
	}); err != nil {
		t.Fatalf("WithOperation: %v", err)
	}
	if !ran {
		t.Fatal("WithOperation did not run its callback")
	}
}
