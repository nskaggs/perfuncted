//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"github.com/nskaggs/perfuncted/internal/sessionregistry"
	"github.com/spf13/cobra"
	"os"
	"strings"
)

func runNamedSessionCommand(cmd *cobra.Command, root *os.File, mode outputMode, operation, name string, printEnv bool) error {
	if !sessionregistry.ValidName(name) {
		return errors.New("invalid session name")
	}
	if printEnv && mode == outputModeJSON {
		return errors.New("--print-env conflicts with JSON output")
	}
	if operation == "recover" {
		if err := sessionregistry.Recover(root, name); err != nil {
			return err
		}
		if mode == outputModeJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(sessionregistry.Reply{Name: name, State: "recovered"})
		}
		return writeCLIOutput(cmd.OutOrStdout(), "%s recovered\n", name)
	}
	reply, err := sessionregistry.Call(cmd.Context(), root, name, operation)
	if err != nil {
		return err
	}
	if mode == outputModeJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(reply)
	}
	if operation == "attach" {
		for _, key := range []string{"XDG_RUNTIME_DIR", "WAYLAND_DISPLAY", "DBUS_SESSION_BUS_ADDRESS"} {
			if writeErr := writeCLIOutput(cmd.OutOrStdout(), "export %s=%s\n", key, namedShellQuote(reply.Environment[key])); writeErr != nil {
				return writeErr
			}
		}
		return nil
	}
	return writeCLIOutput(cmd.OutOrStdout(), "%s %s %s\n", reply.Name, reply.ID, reply.State)
}

func listNamedSessions(cmd *cobra.Command, root *os.File, mode outputMode) error {
	replies, err := sessionregistry.List(cmd.Context(), root)
	if err != nil {
		return err
	}
	if mode == outputModeJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(replies)
	}
	for _, r := range replies {
		if writeErr := writeCLIOutput(cmd.OutOrStdout(), "%s %s %s\n", r.Name, r.ID, r.State); writeErr != nil {
			return writeErr
		}
	}
	return nil
}

func namedCommandsWithRegistry(registry func() (*os.File, error)) []*cobra.Command {
	commands := make([]*cobra.Command, 0, 5)
	for _, op := range []string{"list", "inspect", "attach", "stop", "recover"} {
		operation := op
		var output string
		var printEnv bool

		args := cobra.ExactArgs(1)
		use := operation + " NAME"
		if operation == "list" {
			args = cobra.NoArgs
			use = operation
		}
		cmd := &cobra.Command{Use: use, Short: "Manage named foreground headless sessions: " + operation, Args: args, RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := parseOutputMode(output)
			if err != nil {
				return err
			}
			root, err := registry()
			if err != nil {
				return err
			}
			defer root.Close()
			if operation == "list" {
				return listNamedSessions(cmd, root, mode)
			}
			return runNamedSessionCommand(cmd, root, mode, operation, args[0], printEnv)
		}}
		if operation == "recover" {
			cmd.Long = "Remove only known registry artifacts from a stale named reservation after obtaining its exclusive owner lock. Unpublished reservations may contain partial temporary metadata; published records must have a verified absent owner or a reused PID with a different valid start time. Unsafe or uncertain identities are refused. No process or desktop runtime is removed."
		}
		cmd.Flags().StringVar(&output, "output", "plain", "plain|json")

		if operation == "attach" {
			cmd.Flags().BoolVar(&printEnv, "print-env", false, "emit shell exports (also the plain-output default)")
		}
		commands = append(commands, cmd)
	}
	return commands
}

func namedCommands() []*cobra.Command {
	return namedCommandsWithRegistry(sessionregistry.Registry)
}

func namedShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
