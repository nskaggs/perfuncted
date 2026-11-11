package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/nskaggs/perfuncted"
	"github.com/spf13/cobra"
)

func kdeTileCommands(openPF sessionOpener) []*cobra.Command {
	var x, y int
	tile := &cobra.Command{
		Use:   "tile <window-title>",
		Short: "Place a window in an existing KWin custom tile at a compositor-logical point",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return fmt.Errorf("window title cannot be blank")
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			handle, err := findWindowByTitle(cmd.Context(), pf.Windows, args[0])
			if err != nil {
				return err
			}
			id := handle.ID().String()
			err = pf.Windows.TileWindowByID(cmd.Context(), id, x, y)
			if err != nil {
				return err
			}
			if err = waitForKWinState(cmd.Context(), func(ctx context.Context) (bool, error) {
				info, readErr := handle.Info(ctx)
				if readErr != nil {
					return false, readErr
				}
				return info.Tiled, nil
			}); err != nil {
				return fmt.Errorf("native window tiling not confirmed: %w", err)
			}
			return writeCLIOutput(cmd.OutOrStdout(), "tiled window %s at %d,%d\n", id, x, y)
		},
	}
	tile.Flags().IntVar(&x, "x", 0, "global logical X within desired native tile")
	tile.Flags().IntVar(&y, "y", 0, "global logical Y within desired native tile")
	float := &cobra.Command{
		Use:   "float <window-title>",
		Short: "Remove a window from native KWin tiling, restoring floating behavior",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return fmt.Errorf("window title cannot be blank")
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			handle, err := findWindowByTitle(cmd.Context(), pf.Windows, args[0])
			if err != nil {
				return err
			}
			id := handle.ID().String()
			err = pf.Windows.FloatWindowByID(cmd.Context(), id)
			if err != nil {
				return err
			}
			if err = waitForKWinState(cmd.Context(), func(ctx context.Context) (bool, error) {
				info, readErr := handle.Info(ctx)
				if readErr != nil {
					return false, readErr
				}
				return !info.Tiled, nil
			}); err != nil {
				return fmt.Errorf("native window floating not confirmed: %w", err)
			}
			return writeCLIOutput(cmd.OutOrStdout(), "floating window %s\n", id)
		},
	}
	return []*cobra.Command{tile, float}
}

var _ *perfuncted.WindowBundle
