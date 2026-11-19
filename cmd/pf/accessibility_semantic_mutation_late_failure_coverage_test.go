package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
)

type semanticMutationRejectionProvider struct {
	cliAutomationFake
	failure    error
	focused    int
	textWrites int
}

func (b *semanticMutationRejectionProvider) GrabFocus(context.Context, accessibility.NodeID) error {
	b.focused++
	return b.failure
}
func (b *semanticMutationRejectionProvider) SetTextContents(context.Context, accessibility.NodeID, string) error {
	b.textWrites++
	return b.failure
}

// Having selected an accessible node does not prove that the mutation was
// accepted by its provider; never print success after a rejected operation.
func TestAccessibilitySemanticFocusAndTextPreserveMutationRejection(t *testing.T) {
	denied := errors.New("synthetic AT-SPI mutation rejected")
	for _, tc := range []struct {
		name                string
		args                []string
		wantFocus, wantText int
	}{
		{name: "focus refused", args: []string{"a11y", "focus", "--app", "Test", "--role", "button"}, wantFocus: 1},
		{name: "text refused", args: []string{"a11y", "text", "--app", "Test", "--role", "entry", "--value", "secret"}, wantText: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &semanticMutationRejectionProvider{failure: denied}
			opens := 0
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, backend), nil
				}
			})
			if code == 0 || out != "" || !strings.Contains(errOut, denied.Error()) || opens != 1 || backend.focused != tc.wantFocus || backend.textWrites != tc.wantText {
				t.Fatalf("rejected mutation: code=%d out=%q err=%q opens=%d focus=%d text=%d", code, out, errOut, opens, backend.focused, backend.textWrites)
			}
		})
	}
}
