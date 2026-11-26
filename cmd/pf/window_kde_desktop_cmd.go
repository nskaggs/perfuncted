package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/window"
	"github.com/spf13/cobra"
	"strconv"
	"strings"
	"time"
)

//nolint:gocyclo // Registration of independent desktop subcommands contributes branches to the command factory.
func desktopCmd(openPF sessionOpener) *cobra.Command {
	root := &cobra.Command{Use: "desktop", Short: "Inspect and change compositor virtual desktops"}
	format := "plain"
	list := &cobra.Command{Use: "list", Short: "List virtual desktops with their stable IDs", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format != "plain" && format != "json" {
				return fmt.Errorf("unknown output format %q", format)
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			desktops, err := pf.Windows().WithContext(cmd.Context()).ListDesktops()
			if err != nil {
				return err
			}
			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(desktops)
			}
			for _, d := range desktops {
				if err := writeCLIOutput(cmd.OutOrStdout(), "%d\t%t\t%s\t%s\n", d.Position, d.Current, d.ID, d.Name); err != nil {
					return err
				}
			}
			return nil
		},
	}
	list.Flags().StringVar(&format, "output", "plain", "plain|json")
	var verify bool
	switchTo := &cobra.Command{Use: "switch <desktop-id>", Short: "Switch to a virtual desktop by stable ID",
		Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return fmt.Errorf("desktop ID must not be blank")
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if switchErr := pf.Windows().WithContext(cmd.Context()).SwitchDesktop(args[0]); switchErr != nil {
				return switchErr
			}
			if verify {
				err = waitForKWinState(cmd.Context(), func(ctx context.Context) (bool, error) {
					desktops, lookupErr := pf.Windows().WithContext(ctx).ListDesktops()
					if lookupErr != nil {
						return false, lookupErr
					}
					for _, d := range desktops {
						if d.ID == args[0] {
							return d.Current, nil
						}
					}
					return false, window.ErrDesktopNotFound
				})
				if err != nil {
					return fmt.Errorf("virtual desktop switch verification: %w", err)
				}
			}
			return writeCLIOutput(cmd.OutOrStdout(), "switched to desktop %s\n", args[0])
		},
	}
	switchTo.Flags().BoolVar(&verify, "verify", true, "confirm current desktop changed")
	root.AddCommand(list, switchTo)
	return root
}

func waitForKWinState(ctx context.Context, probe func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := probe(ctx)
		if err != nil {
			return errors.Join(ctx.Err(), err)
		}
		if ok {
			// A positive compositor observation does not authorize success
			// after the caller has canceled the verification request.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

//nolint:gocyclo // Each GUI subcommand has independent validation and explicit fail-closed capability handling.
func kdeWindowExtraCommands(openPF sessionOpener) []*cobra.Command {
	state := &cobra.Command{Use: "active-info", Short: "Show compositor state of the active window as JSON", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			active := true
			handles, err := pf.Windows().WithContext(cmd.Context()).List(window.Match{Active: &active})
			if err != nil {
				return err
			}
			if len(handles) != 1 {
				return fmt.Errorf("expected one active window, found %d: %w", len(handles), window.ErrWindowNotFound)
			}
			info, err := handles[0].WithContext(cmd.Context()).Info()
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
		},
	}
	cursor := &cobra.Command{Use: "cursor", Short: "Query actual compositor cursor coordinates", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			x, y, err := pf.Windows().WithContext(cmd.Context()).CursorPosition()
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%d,%d\n", x, y)
		},
	}
	var verifySet bool
	setDesktop := &cobra.Command{Use: "set-desktop <window-title> <desktop-id>",
		Short: "Move a window to an existing virtual desktop",
		Args:  cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" || strings.TrimSpace(args[1]) == "" {
				return fmt.Errorf("title and desktop ID must not be empty")
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			handle, err := findWindowByTitle(cmd.Context(), pf.Windows(), args[0])
			if err != nil {
				return err
			}
			id := handle.ID().String()
			if moveErr := pf.Windows().WithContext(cmd.Context()).SetWindowDesktopByID(id, args[1]); moveErr != nil {
				return moveErr
			}
			if verifySet {
				err = waitForKWinState(cmd.Context(), func(ctx context.Context) (bool, error) {
					info, lookupErr := handle.WithContext(ctx).Info()
					if lookupErr != nil {
						return false, lookupErr
					}
					for _, d := range strings.Split(info.DesktopIDs, ",") {
						if d == args[1] {
							return true, nil
						}
					}
					return false, nil
				})
				if err != nil {
					return fmt.Errorf("window desktop assignment verification: %w", err)
				}
			}
			return writeCLIOutput(cmd.OutOrStdout(), "moved window %s to desktop %s\n", id, args[1])
		},
	}
	setDesktop.Flags().BoolVar(&verifySet, "verify", true, "verify assigned window desktop")

	newStacking := func(above bool) *cobra.Command {
		label := "keep-below"
		if above {
			label = "keep-above"
		}
		var verify bool
		c := &cobra.Command{Use: label + " <window-title> <on|off>", Short: "Change KWin window stacking state",
			Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
				var on bool
				switch strings.ToLower(args[1]) {
				case "on":
					on = true
				case "off":
					on = false
				default:
					return fmt.Errorf("state must be on or off")
				}
				pf, err := openPF(cmd.Context())
				if err != nil {
					return err
				}
				defer pf.Close()
				handle, err := findWindowByTitle(cmd.Context(), pf.Windows(), args[0])
				if err != nil {
					return err
				}
				id := handle.ID().String()
				if above {
					err = pf.Windows().WithContext(cmd.Context()).SetKeepAboveByID(id, on)
				} else {
					err = pf.Windows().WithContext(cmd.Context()).SetKeepBelowByID(id, on)
				}
				if err != nil {
					return err
				}
				if verify {
					err = waitForKWinState(cmd.Context(), func(ctx context.Context) (bool, error) {
						info, lookupErr := handle.WithContext(ctx).Info()
						if lookupErr != nil {
							return false, lookupErr
						}
						if above {
							return info.KeepAbove == on, nil
						}
						return info.KeepBelow == on, nil
					})
					if err != nil {
						return fmt.Errorf("%s verification: %w", label, err)
					}
				}
				return writeCLIOutput(cmd.OutOrStdout(), "%s %s: %s\n", label, strconv.FormatBool(on), id)
			},
		}
		c.Flags().BoolVar(&verify, "verify", true, "verify updated stacking state")
		return c
	}
	shade := &cobra.Command{Use: "shade <window-title> <on|off>", Short: "Toggle true KWin titlebar shading where supported",
		Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			var on bool
			switch strings.ToLower(args[1]) {
			case "on":
				on = true
			case "off":
				on = false
			default:
				return fmt.Errorf("shade state must be on or off")
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := findWindowByTitle(cmd.Context(), pf.Windows(), args[0])
			if err != nil {
				return err
			}
			id := target.ID().String()
			if err := pf.Windows().WithContext(cmd.Context()).SetShadeByID(id, on); err != nil {
				return err
			}
			if err := waitForKWinState(cmd.Context(), func(ctx context.Context) (bool, error) {
				info, err := target.WithContext(ctx).Info()
				if err != nil {
					return false, err
				}
				return info.Shaded == on, nil
			}); err != nil {
				return fmt.Errorf("shade verification: %w", err)
			}
			return writeCLIOutput(cmd.OutOrStdout(), "shade %t: %s\n", on, id)
		},
	}
	return append([]*cobra.Command{state, cursor, setDesktop, newStacking(true), newStacking(false), shade}, kdeTileCommands(openPF)...)
}

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
			handle, err := findWindowByTitle(cmd.Context(), pf.Windows(), args[0])
			if err != nil {
				return err
			}
			id := handle.ID().String()
			err = pf.Windows().WithContext(cmd.Context()).TileWindowByID(id, x, y)
			if err != nil {
				return err
			}
			if err = waitForKWinState(cmd.Context(), func(ctx context.Context) (bool, error) {
				info, readErr := handle.WithContext(ctx).Info()
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
			handle, err := findWindowByTitle(cmd.Context(), pf.Windows(), args[0])
			if err != nil {
				return err
			}
			id := handle.ID().String()
			err = pf.Windows().WithContext(cmd.Context()).FloatWindowByID(id)
			if err != nil {
				return err
			}
			if err = waitForKWinState(cmd.Context(), func(ctx context.Context) (bool, error) {
				info, readErr := handle.WithContext(ctx).Info()
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

var _ perfuncted.WindowBundle
