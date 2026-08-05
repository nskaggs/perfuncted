package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func clipboardCmd(openPF sessionOpener) *cobra.Command {
	cmd := &cobra.Command{Use: "clipboard", Short: "Clipboard operations"}

	get := &cobra.Command{
		Use:   "get",
		Short: "Print clipboard contents",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			s, err := pf.Clipboard.Get(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), s)
			return nil
		},
	}

	set := &cobra.Command{
		Use:   "set <text>",
		Short: "Set clipboard contents",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			return pf.Clipboard.Set(cmd.Context(), args[0])
		},
	}

	cmd.AddCommand(get, set)

	return cmd
}

// ── helpers ───────────────────────────────────────────────────────────────────────────
