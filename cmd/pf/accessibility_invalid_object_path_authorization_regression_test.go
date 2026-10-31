package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

// AT-SPI node handles must be real D-Bus object paths. Malformed paths are
// statically invalid and must not trigger a desktop authorization prompt.
func TestAccessibilityRawMalformedObjectPathsNeverOpenDesktopSession(t *testing.T) {
	for _, path := range []string{"node", "/node//child", "/node/../child", "/node-with-hyphen"} {
		t.Run(path, func(t *testing.T) {
			opened := 0
			args := []string{"a11y", "raw", "--op", "focus", "--bus", "org.example.App", "--path", path, "--generation", "1"}
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("unexpected desktop access")
				}
			})
			if code == 0 || opened != 0 || out != "" || !strings.Contains(stderr, "valid D-Bus object path") {
				t.Fatalf("invalid path %q opened=%d code=%d stdout=%q stderr=%q", path, opened, code, out, stderr)
			}
		})
	}
}
