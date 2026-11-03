package main

import (
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

func TestScriptWindowTargetSelectionRequiresExplicitOrPriorSelection(t *testing.T) {
	selected := &perfuncted.Window{}
	runner := &scriptRunner{selectedWindow: selected}

	for _, tc := range []struct {
		name    string
		resolve func() (*perfuncted.Window, error)
	}{
		{name: "no arguments use prior selection", resolve: func() (*perfuncted.Window, error) { return runner.windowTarget(nil) }},
		{name: "empty title uses prior selection", resolve: func() (*perfuncted.Window, error) { return runner.targetForTitle("") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.resolve()
			if err != nil || got != selected {
				t.Fatalf("selected target = %p, %v; want %p", got, err, selected)
			}
		})
	}

	runner.selectedWindow = nil
	for _, tc := range []struct {
		name    string
		resolve func() (*perfuncted.Window, error)
	}{
		{name: "no arguments", resolve: func() (*perfuncted.Window, error) { return runner.windowTarget(nil) }},
		{name: "empty title", resolve: func() (*perfuncted.Window, error) { return runner.targetForTitle("") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.resolve()
			if got != nil || err == nil || !strings.Contains(err.Error(), "requires a title or a prior window select") {
				t.Fatalf("missing selection = %p, %v; want explicit error", got, err)
			}
		})
	}
}
