package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// A half-specified pointer location must not silently send an input action
// at the previous cursor position: the caller believes a different target
// was selected. Reject these commands before a consent-prompting session opens.
func TestInputCLIRejectsPartialMouseCoordinatesAndNegativeClickRepeatsBeforeAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"mouse down only x", []string{"input", "mousedown", "--x", "15"}},
		{"mouse down only y", []string{"input", "mousedown", "--y", "25"}},
		{"mouse up only x", []string{"input", "mouseup", "--x", "15"}},
		{"mouse up only y", []string{"input", "mouseup", "--y", "25"}},
		{"negative click repeat", []string{"input", "click", "--repeat", "-2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			stdout, stderr, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("must not open desktop session")
				}
			})
			if exit == 0 || opened != 0 || stdout != "" || stderr == "" || strings.Contains(stderr, "must not open desktop session") {
				t.Fatalf("ambiguous input accepted: args=%v opened=%d exit=%d stdout=%q stderr=%q", tc.args, opened, exit, stdout, stderr)
			}
		})
	}
}
