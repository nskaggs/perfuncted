//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"github.com/nskaggs/perfuncted/internal/sessionregistry"
	"github.com/spf13/cobra"
)

func workflowCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "workflow",
		Short: "Control an active same-user script",
		Long: `Control a script started with pf run --workflow-id ID.

The control socket and status record live below /tmp/pf-workflows-<uid> and
are private to the current user. Pause is cooperative: status first reports
pause-requested and becomes paused only after the current command reaches the
next script boundary. Stop cancels in-flight context-aware work and waits up to
five seconds for the run and Session cleanup; if it is still stopping, query
status again. Status JSON includes the stable ID, state, last completed step,
current step/phase, and a sanitized terminal error category. It never includes
script contents or typed input. A terminal stopped/failed/completed state can
be read after the socket closes. A process crash has no trustworthy terminal
status, and an unknown or stale endpoint fails closed.

These controls do not reverse operations already delivered to the desktop and
do not provide a global hotkey or process supervisor.`,
	}
	for _, operation := range []string{"status", "pause", "resume", "stop"} {
		var output string
		short := operation + " an active workflow"
		if operation == "status" {
			short = "show the status of an active workflow"
		}
		cmd := &cobra.Command{Use: operation + " ID", Short: short, Args: cobra.ExactArgs(1), Example: "  pf workflow " + operation + " task-1", RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := parseOutputMode(output)
			if err != nil {
				return err
			}
			root, err := sessionregistry.WorkflowRegistry()
			if err != nil {
				return err
			}
			defer root.Close()
			reply, err := sessionregistry.CallWorkflow(cmd.Context(), root, args[0], operation)
			if err != nil {
				return err
			}
			if mode == outputModeJSON {
				err = json.NewEncoder(cmd.OutOrStdout()).Encode(reply)
			} else {
				err = writeCLIOutput(cmd.OutOrStdout(), "%s %s\n", reply.ID, reply.State)
			}
			if err != nil {
				return err
			}
			if !reply.Accepted {
				return errors.New("workflow operation not accepted")
			}
			return nil
		}}
		cmd.Flags().StringVar(&output, "output", "plain", "plain|json")
		group.AddCommand(cmd)
	}
	return group
}
