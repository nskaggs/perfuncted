package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
)

type syntheticSemanticRootFailure struct {
	cliAutomationFake
	failure error
}

//nolint:unparam // This synthetic provider always denies scope resolution and intentionally returns no application.
func (s syntheticSemanticRootFailure) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	return accessibility.Application{}, s.failure
}

// Semantic mutations must fail before resolving or changing an element if
// application scope cannot be authenticated by the accessibility provider.
func TestAccessibilitySemanticMutationsRejectFailedScopeResolution(t *testing.T) {
	denied := errors.New("synthetic application scope unavailable")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "action", args: []string{"a11y", "action", "--app", "Test", "--role", "button"}},
		{name: "focus", args: []string{"a11y", "focus", "--app", "Test", "--role", "button"}},
		{name: "text", args: []string{"a11y", "text", "--app", "Test", "--role", "entry", "--value", "secret"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			stdout, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, syntheticSemanticRootFailure{failure: denied}), nil
				}
			})
			if code == 0 || stdout != "" || !strings.Contains(stderr, denied.Error()) || opens != 1 {
				t.Fatalf("semantic mutation accepted invalid scope: code=%d stdout=%q stderr=%q opens=%d", code, stdout, stderr, opens)
			}
		})
	}
}
