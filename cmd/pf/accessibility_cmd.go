package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/spf13/cobra"
)

type accessibilityCLIOptions struct {
	output         string
	rootBus        string
	rootPath       string
	appName        string
	pid            int32
	maxDepth       int
	maxNodes       int
	maxTextBytes   int
	allowSensitive bool
}

func (o accessibilityCLIOptions) root(ctx context.Context, pf *perfuncted.Session) (accessibility.NodeID, error) {
	if strings.TrimSpace(o.appName) == "" && o.pid == 0 && strings.TrimSpace(o.rootBus) == "" && strings.TrimSpace(o.rootPath) == "" {
		return accessibility.NodeID{}, nil
	}
	if o.appName != "" || o.pid != 0 {
		app, err := pf.Accessibility.FindApplication(ctx, accessibility.ApplicationFilter{Name: o.appName, PID: o.pid, Bus: o.rootBus})
		if err != nil {
			return accessibility.NodeID{}, err
		}
		return app.ID, nil
	}
	if o.rootBus == "" || o.rootPath == "" {
		return accessibility.NodeID{}, fmt.Errorf("root requires both --root-bus and --root-path")
	}
	return accessibility.NodeID{BusName: o.rootBus, ObjectPath: o.rootPath}, nil
}

func (o accessibilityCLIOptions) snapshot() accessibility.SnapshotOptions {
	return accessibility.SnapshotOptions{MaxDepth: o.maxDepth, MaxNodes: o.maxNodes, MaxTextBytes: o.maxTextBytes, AllowSensitive: o.allowSensitive}
}

func accessibilityOutput(w io.Writer, format string, value any) error {
	if strings.EqualFold(format, "json") {
		return json.NewEncoder(w).Encode(value)
	}
	return fmt.Errorf("unknown output format %q (want json)", format)
}

func accessibilityCmd(openPF sessionOpener) *cobra.Command {
	cmd := &cobra.Command{Use: "accessibility", Aliases: []string{"a11y"}, Short: "Inspect the AT-SPI accessibility tree"}
	common := func(c *cobra.Command, o *accessibilityCLIOptions) {
		c.Flags().StringVar(&o.output, "output", "json", "output format (json)")
		c.Flags().StringVar(&o.rootBus, "root-bus", "", "AT-SPI application bus name")
		c.Flags().StringVar(&o.rootPath, "root-path", "", "AT-SPI application object path")
		c.Flags().StringVar(&o.appName, "app", "", "application accessible-name substring")
		c.Flags().Int32Var(&o.pid, "pid", 0, "application process ID")
		c.Flags().IntVar(&o.maxDepth, "max-depth", 0, "maximum tree depth")
		c.Flags().IntVar(&o.maxNodes, "max-nodes", 0, "maximum nodes")
		c.Flags().IntVar(&o.maxTextBytes, "max-text-bytes", 0, "maximum text bytes per node")
		c.Flags().BoolVar(&o.allowSensitive, "allow-sensitive", false, "include sensitive/protected text (use with care)")
	}

	var appOpts accessibilityCLIOptions
	applications := &cobra.Command{Use: "applications", Short: "List registered accessible applications", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		apps, err := pf.Accessibility.Applications(c.Context())
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), appOpts.output, apps)
	}}
	common(applications, &appOpts)

	var snapOpts accessibilityCLIOptions
	snapshot := &cobra.Command{Use: "snapshot", Short: "Capture a bounded accessibility tree", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		root, err := snapOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		value, err := pf.Accessibility.Snapshot(c.Context(), root, snapOpts.snapshot())
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), snapOpts.output, value)
	}}
	common(snapshot, &snapOpts)

	var findOpts accessibilityCLIOptions
	var query accessibility.Query
	findCmd := &cobra.Command{Use: "find", Short: "Find accessible nodes by name, role, or text", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		root, err := findOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		value, err := pf.Accessibility.Find(c.Context(), root, query, findOpts.snapshot())
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), findOpts.output, value)
	}}
	common(findCmd, &findOpts)
	findCmd.Flags().StringVar(&query.Name, "name", "", "accessible name substring")
	findCmd.Flags().StringVar(&query.Role, "role", "", "accessible role substring")
	findCmd.Flags().StringVar(&query.Text, "text", "", "accessible text substring")

	focused := &cobra.Command{Use: "focused", Short: "Print the currently focused accessible node", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		value, err := pf.Accessibility.Focused(c.Context(), appOpts.snapshot())
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), appOpts.output, value)
	}}
	common(focused, &appOpts)

	var pointX, pointY int
	atPoint := &cobra.Command{Use: "at-point", Short: "Inspect the accessible object at a screen coordinate", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		value, err := pf.Accessibility.AtPoint(c.Context(), pointX, pointY)
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), appOpts.output, value)
	}}
	common(atPoint, &appOpts)
	atPoint.Flags().IntVar(&pointX, "x", 0, "screen x coordinate")
	atPoint.Flags().IntVar(&pointY, "y", 0, "screen y coordinate")

	cmd.AddCommand(applications, snapshot, findCmd, focused, atPoint)
	return cmd
}
