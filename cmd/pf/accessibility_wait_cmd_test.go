package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
)

type cliWaitAccessibilityFake struct{ cliAutomationFake }

func (cliWaitAccessibilityFake) FindApplicationFresh(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	return accessibility.Application{Node: accessibility.Node{
		ID: accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 1}, Name: "Test App", Role: "application",
	}, PID: 123}, nil
}

func (cliWaitAccessibilityFake) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	return make(chan accessibility.Event), nil
}

func TestAccessibilityWaitCLIRequiresTimeoutBeforeOpeningSession(t *testing.T) {
	opened := 0
	factory := func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			opened++
			return nil, nil
		}
	}
	for _, args := range [][]string{
		{"a11y", "wait", "--app", "Test", "--role", "button", "--name", "Save"},
		{"a11y", "wait", "--app", "Test", "--until", "made-up", "--timeout", "1s"},
		{"a11y", "wait", "--app", "Test", "--timeout", "1s", "--poll", "-1ms"},
		{"a11y", "wait", "--app", "Test", "--until", "hidden", "--timeout", "1s", "--visible-only"},
		{"a11y", "wait", "--app", "Test", "--until", "absent", "--timeout", "1s", "--visible-only"},
	} {
		_, stderr, code := captureRunIO(t, args, factory)
		if code == 0 || stderr == "" {
			t.Errorf("invalid wait arguments %v: code=%d stderr=%q", args, code, stderr)
		}
	}
	if opened != 0 {
		t.Fatalf("preflight failures opened %d desktop sessions", opened)
	}
}

func TestAccessibilityWaitCLIJSONUsesScopedFreshWait(t *testing.T) {
	open := func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliWaitAccessibilityFake{}), nil
	}
	stdout, stderr, code := captureRunIO(t,
		[]string{"a11y", "wait", "--app", "Test", "--pid", "123", "--role", "button", "--name", "Save", "--until", "present", "--timeout", "1s", "--json"},
		func(*cliConfig) sessionOpener { return open },
	)
	if code != 0 || stderr != "" {
		t.Fatalf("wait command code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	var result accessibilityWaitCLIResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("wait JSON %q: %v", stdout, err)
	}
	if !result.Satisfied || result.State != perfuncted.AccessibilityWaitPresent || result.Node == nil || result.Node.Name != "Save" || result.Evidence.Evaluations != 1 {
		t.Fatalf("wait JSON result = %+v", result)
	}
}

func TestAccessibilityWaitCommandAppearsInHelp(t *testing.T) {
	root := newRootCmd(func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return nil, nil }
	})
	command := findCommandPath(root, "a11y", "wait")
	if command == nil {
		t.Fatal("missing pf a11y wait")
	}
	if !strings.Contains(command.Long, "fresh complete snapshots") || command.Flags().Lookup("timeout") == nil || command.Flags().Lookup("until") == nil {
		t.Fatalf("wait command contract incomplete: long=%q flags=%v", command.Long, command.Flags().FlagUsages())
	}
}
