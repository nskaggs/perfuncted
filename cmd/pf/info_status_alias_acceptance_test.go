package main

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/spf13/cobra"
)

// Public info and status must use exactly the same session capability probe,
// with no stale independent status cache or differences in JSON rendering.
func TestInfoStatusAliasesReportIdenticalCapabilities(t *testing.T) {
	opens := 0
	newRoot := func() *cobra.Command {
		return newRootCmd(func(*cliConfig) sessionOpener {
			return func(context.Context) (*perfuncted.Session, error) {
				opens++
				return pftest.New(nil, nil, nil, nil), nil
			}
		})
	}
	var old, alias map[string]any
	for i, tc := range []struct {
		command string
		target  *map[string]any
	}{{"info", &old}, {"status", &alias}} {
		cmd := newRoot()
		cmd.SetArgs([]string{tc.command, "--output=json"})
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s: %v", tc.command, err)
		}
		if err := json.Unmarshal(out.Bytes(), tc.target); err != nil {
			t.Fatalf("%s invalid JSON: %v: %s", tc.command, err, out.String())
		}
		if _, ok := (*tc.target)["capabilities"]; !ok {
			t.Fatalf("%s missing capabilities", tc.command)
		}
		if opens != i+1 {
			t.Fatalf("%s opened session %d times; want %d", tc.command, opens, i+1)
		}
	}
	if !reflect.DeepEqual(old, alias) {
		t.Fatalf("info and status JSON differed: info=%v status=%v", old, alias)
	}
}
