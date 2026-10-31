package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// An empty document-selection batch cannot be applied by the native AT-SPI
// backend. Reject it before desktop authorization rather than opening a
// session only to discover a known-invalid request after connecting.
func TestAccessibilityEmptyDocumentSelectionsRejectBeforeDesktopAuthorization(t *testing.T) {
	for _, input := range []string{"[]", "null", " [] ", "\nnull"} {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			opens := 0
			args := []string{"a11y", "raw", "--bus", "org.test.App", "--path", "/editor", "--generation", "1", "--op", "set-document-text-selections", "--selections", input}
			stdout, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("desktop authorization reached")
				}
			})
			if code == 0 || opens != 0 || stdout != "" || !strings.Contains(stderr, "must not be empty") {
				t.Fatalf("empty selection=%q: opened=%d code=%d stdout=%q stderr=%q", input, opens, code, stdout, stderr)
			}
		})
	}
}
