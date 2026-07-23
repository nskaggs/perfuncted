// Used by integration tests (build tag: integration).
// This file exists so deadcode -test ./... sees them as reachable.

package x11test

import "testing"

func TestHelpersCompile(t *testing.T) {
	_, stop, err := StartXvfb()
	if err != nil {
		t.Logf("StartXvfb expected to fail without display: %v", err)
	}
	if stop != nil {
		stop()
	}

	_, stop, err = StartXephyr(":99")
	if err != nil {
		t.Logf("StartXephyr expected to fail without display: %v", err)
	}
	if stop != nil {
		stop()
	}
}
