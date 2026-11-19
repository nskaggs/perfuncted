package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
)

type screenClickFailingOutput struct {
	*outputTestLister
	failure error
}

func (o *screenClickFailingOutput) List(context.Context) ([]output.Info, error) {
	return nil, o.failure
}

type screenClickSyncFailureInput struct {
	*coordinateTestInput
	syncErr error
}

func (i *screenClickSyncFailureInput) Sync(context.Context) error { return i.syncErr }

// Screen-coordinate clicking is never complete until discovery, delivery and
// requested synchronization have succeeded. A rejected operation cannot emit
// a success line even if an earlier stage made progress.
func TestClickScreenPropagatesDiscoveryInjectionAndSyncFailures(t *testing.T) {
	failure := errors.New("synthetic screen click stage failed")
	for _, tc := range []struct {
		name                               string
		outputError, clickError, syncError error
		wantCalls                          int
	}{
		{name: "output discovery", outputError: failure},
		{name: "pointer injection", clickError: failure, wantCalls: 1},
		{name: "input synchronization", syncError: failure, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			injected := &pftest.Inputter{Err: tc.clickError}
			in := &screenClickSyncFailureInput{coordinateTestInput: &coordinateTestInput{Inputter: injected, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}, syncErr: tc.syncError}
			outs := &screenClickFailingOutput{outputTestLister: &outputTestLister{outs: []output.Info{{Available: true, Geometry: output.Geometry{W: 100, H: 100}, ResolutionW: 100, ResolutionH: 100}}}, failure: tc.outputError}
			var provider output.Lister = outs
			if tc.outputError == nil {
				provider = &outputTestLister{outs: []output.Info{{Available: true, Geometry: output.Geometry{W: 100, H: 100}, ResolutionW: 100, ResolutionH: 100}}}
			}
			sessionOpens := 0
			opener := func(context.Context) (*perfuncted.Session, error) {
				sessionOpens++
				return pftest.NewWithOutputs(nil, in, nil, provider, nil), nil
			}
			cmd := inputCmd(opener, &cliConfig{sync: true}, nil, opener)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs([]string{"click-screen", "--x", "12", "--y", "34"})
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, failure) {
				t.Fatalf("stage failure not propagated: %v", err)
			}
			if stdout.Len() != 0 || sessionOpens != 1 || len(injected.Calls) != tc.wantCalls {
				t.Fatalf("failed screen click published %q, opened %d sessions, calls=%v", stdout.String(), sessionOpens, injected.Calls)
			}
		})
	}
}
