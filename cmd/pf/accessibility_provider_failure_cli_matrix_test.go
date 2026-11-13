package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
)

type deniedAccessibilityReads struct {
	cliAccessibilityFake
	failure error
	calls   int
}

func (f *deniedAccessibilityReads) Applications(context.Context) ([]accessibility.Application, error) {
	f.calls++
	return nil, f.failure
}
func (f *deniedAccessibilityReads) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	f.calls++
	return accessibility.Application{}, f.failure
}
func (f *deniedAccessibilityReads) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	f.calls++
	return accessibility.Snapshot{}, f.failure
}
func (f *deniedAccessibilityReads) Find(context.Context, accessibility.NodeID, accessibility.Query, accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	f.calls++
	return nil, f.failure
}
func (f *deniedAccessibilityReads) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	f.calls++
	return accessibility.Node{}, f.failure
}
func (f *deniedAccessibilityReads) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	f.calls++
	return accessibility.Node{}, f.failure
}
func (f *deniedAccessibilityReads) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	f.calls++
	return nil, f.failure
}

// Provider failures must never be mistaken for an empty tree or a negative
// query result. Every read command stops at the first failed provider call.
func TestAccessibilityReadCommandsReturnProviderFailuresWithoutResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"apps", []string{"a11y", "apps"}},
		{"tree application resolution", []string{"a11y", "tree", "--app", "Editor"}},
		{"find application resolution", []string{"a11y", "find", "--app", "Editor", "--role", "button"}},
		{"focused", []string{"a11y", "focused"}},
		{"at point", []string{"a11y", "at-point", "10", "20"}},
		{"events", []string{"a11y", "events"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &deniedAccessibilityReads{failure: errors.New("synthetic inaccessible provider")}
			opens := 0
			stdout, stderr, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, f), nil
				}
			})
			if exit == 0 || stdout != "" || !strings.Contains(stderr, f.failure.Error()) || opens != 1 || f.calls != 1 {
				t.Fatalf("provider failure swallowed args=%v exit=%d stdout=%q stderr=%q opens=%d provider_calls=%d", tc.args, exit, stdout, stderr, opens, f.calls)
			}
		})
	}
}
