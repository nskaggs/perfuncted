package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

// The default user-facing info command must render the capability report,
// and a disconnected consumer must not be treated as a successful report.
func TestInfoDefaultPlainCommandReportsCapabilitiesAndWriteFailures(t *testing.T) {
	t.Run("default plain output", func(t *testing.T) {
		opens := 0
		cmd := infoCmd(func(context.Context) (*perfuncted.Session, error) {
			opens++
			return pftest.New(nil, nil, nil, nil), nil
		})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("default info command: %v", err)
		}
		if opens != 1 || !strings.Contains(out.String(), "Environment") || !strings.Contains(out.String(), "Capabilities") {
			t.Fatalf("default info lost plain sections or opened %d times: %q", opens, out.String())
		}
	})
	t.Run("disconnected stdout", func(t *testing.T) {
		opens := 0
		cmd := infoCmd(func(context.Context) (*perfuncted.Session, error) {
			opens++
			return pftest.New(nil, nil, nil, nil), nil
		})
		cmd.SetOut(&infoFailingWriter{max: 1})
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, io.ErrClosedPipe) || opens != 1 {
			t.Fatalf("closed plain report output: err=%v opens=%d", err, opens)
		}
	})
}
