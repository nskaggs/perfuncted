package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/spf13/cobra"
)

func inputStateCmd(openObserver sessionOpener) *cobra.Command {
	var keyName, lockName, buttonName, output string
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Query one current key, lock, or mouse-button state",
		Long: `Query one input state on demand through a read-only observation backend.
On X11, results report aggregate logical state with unknown physical-versus-
synthetic provenance. They do not record key history or infer state from prior
Perfuncted input commands. Other backends return an explicit unsupported error.
This currently requires native X11 with XI2; Wayland and XWayland are unsupported.
It does not request root or input-device access. Caps Lock's lock state is
separate from whether the Caps Lock key is held.`,
		Example: "pf input state --key shift\npf input state --lock capslock\npf input state --button left",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("unsupported output %q (use json)", output)
			}
			selectors := 0
			for _, name := range []string{"key", "lock", "button"} {
				if cmd.Flags().Changed(name) {
					selectors++
				}
			}
			if selectors != 1 {
				return fmt.Errorf("select exactly one of --key, --lock, or --button")
			}
			if cmd.Flags().Changed("key") {
				if err := input.ValidateKeyStateQuery(keyName); err != nil {
					return err
				}
			} else if cmd.Flags().Changed("lock") {
				if err := input.ValidateLockStateQuery(lockName); err != nil {
					return err
				}
			} else if _, err := parseInputStateButton(buttonName); err != nil {
				return err
			}
			pf, err := openObserver(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			var state input.StateObservation
			switch {
			case cmd.Flags().Changed("key"):
				state, err = pf.InputObservation().KeyState(cmd.Context(), keyName)
			case cmd.Flags().Changed("lock"):
				state, err = pf.InputObservation().LockState(cmd.Context(), lockName)
			default:
				var button int
				button, err = parseInputStateButton(buttonName)
				if err == nil {
					state, err = pf.InputObservation().MouseButtonState(cmd.Context(), button)
				}
			}
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(state)
		},
	}
	cmd.Flags().StringVar(&keyName, "key", "", "named key or modifier to query")
	cmd.Flags().StringVar(&lockName, "lock", "", "lock-key state to query (capslock, numlock, scrolllock)")
	cmd.Flags().StringVar(&buttonName, "button", "", "mouse button to query (left, middle, right, button4, button5)")
	cmd.Flags().StringVar(&output, "output", "json", "output format: json")
	return cmd
}

func parseInputStateButton(value string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "left", "1", "button1":
		return 1, nil
	case "middle", "2", "button2":
		return 2, nil
	case "right", "3", "button3":
		return 3, nil
	case "button4", "4":
		return 4, nil
	case "button5", "5":
		return 5, nil
	default:
		return 0, fmt.Errorf("invalid mouse button %q", value)
	}
}
