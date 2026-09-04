package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/spf13/cobra"
)

type accessibilityCLIOptions struct {
	output         string
	json           bool
	rootBus        string
	rootPath       string
	generation     uint64
	desktopRoot    bool
	appName        string
	pid            int32
	windowID       string
	windowTitle    string
	maxDepth       int
	maxNodes       int
	maxTextBytes   int
	visibleOnly    bool
	allowSensitive bool
}

func (o accessibilityCLIOptions) root(ctx context.Context, pf *perfuncted.Session) (accessibility.NodeID, error) {
	if strings.TrimSpace(o.appName) == "" && o.pid == 0 && strings.TrimSpace(o.rootBus) == "" && strings.TrimSpace(o.rootPath) == "" {
		return accessibility.NodeID{}, nil
	}
	if o.appName != "" || o.pid != 0 {
		app, err := pf.Accessibility.FindApplication(ctx, accessibility.ApplicationFilter{Name: o.appName, PID: o.pid, Bus: o.rootBus, WindowID: o.windowID, WindowTitle: o.windowTitle})
		if err != nil {
			return accessibility.NodeID{}, err
		}
		return app.ID, nil
	}
	if o.rootBus == "" || o.rootPath == "" {
		return accessibility.NodeID{}, fmt.Errorf("root requires both --root-bus and --root-path")
	}
	if o.generation == 0 {
		return accessibility.NodeID{}, fmt.Errorf("root requires --generation from a current accessibility snapshot")
	}
	return accessibility.NodeID{BusName: o.rootBus, ObjectPath: o.rootPath, Generation: o.generation}, nil
}

func (o accessibilityCLIOptions) snapshot() accessibility.SnapshotOptions {
	return accessibility.SnapshotOptions{MaxDepth: o.maxDepth, MaxNodes: o.maxNodes, MaxTextBytes: o.maxTextBytes, VisibleOnly: o.visibleOnly, AllowSensitive: o.allowSensitive, AllowDesktopRoot: o.desktopRoot}
}

func (o accessibilityCLIOptions) format() string {
	if o.json {
		return "json"
	}
	return o.output
}

func accessibilityOutput(w io.Writer, format string, value any) error {
	if strings.EqualFold(format, "json") {
		return json.NewEncoder(w).Encode(value)
	}
	return fmt.Errorf("unknown output format %q (want json)", format)
}

func accessibilityCmd(openPF sessionOpener) *cobra.Command { //nolint:gocyclo // Cobra command assembly is intentionally centralized.
	cmd := &cobra.Command{Use: "accessibility", Aliases: []string{"a11y"}, Short: "Inspect the AT-SPI accessibility tree"}
	common := func(c *cobra.Command, o *accessibilityCLIOptions) {
		c.Flags().StringVar(&o.output, "output", "json", "output format (json)")
		c.Flags().BoolVar(&o.json, "json", false, "output JSON (alias for --output json)")
		c.Flags().StringVar(&o.rootBus, "root-bus", "", "AT-SPI application bus name")
		c.Flags().StringVar(&o.rootPath, "root-path", "", "AT-SPI application object path")
		c.Flags().Uint64Var(&o.generation, "generation", 0, "current accessibility generation for --root-bus/--root-path")
		c.Flags().BoolVar(&o.desktopRoot, "desktop-root", false, "explicitly allow bounded whole-desktop traversal")
		c.Flags().StringVar(&o.appName, "app", "", "application accessible-name substring")
		c.Flags().StringVar(&o.appName, "application", "", "application accessible-name substring (alias for --app)")
		c.Flags().Int32Var(&o.pid, "pid", 0, "application process ID")
		c.Flags().StringVar(&o.windowID, "window-id", "", "managed window identifier")
		c.Flags().StringVar(&o.windowTitle, "window-title", "", "managed window title (exact)")
		c.Flags().IntVar(&o.maxDepth, "max-depth", 0, "maximum tree depth")
		c.Flags().IntVar(&o.maxNodes, "max-nodes", 0, "maximum nodes")
		c.Flags().IntVar(&o.maxTextBytes, "max-text-bytes", 0, "maximum text bytes per node")
		c.Flags().BoolVar(&o.visibleOnly, "visible-only", false, "exclude invisible/off-screen nodes")
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
		return accessibilityOutput(c.OutOrStdout(), appOpts.format(), apps)
	}}
	common(applications, &appOpts)

	var snapOpts accessibilityCLIOptions
	snapshot := &cobra.Command{Use: "snapshot", Aliases: []string{"tree"}, Short: "Capture a bounded accessibility tree", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
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
		return accessibilityOutput(c.OutOrStdout(), snapOpts.format(), value)
	}}
	common(snapshot, &snapOpts)

	var findOpts accessibilityCLIOptions
	var query accessibility.Query
	var attributeFlags []string
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
		query.Attributes = parseAccessibilityAttributes(attributeFlags)
		value, err := pf.Accessibility.Find(c.Context(), root, query, findOpts.snapshot())
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), findOpts.format(), value)
	}}
	common(findCmd, &findOpts)
	findCmd.Flags().StringVar(&query.Name, "name", "", "accessible name substring")
	findCmd.Flags().StringVar(&query.Role, "role", "", "accessible role substring")
	findCmd.Flags().StringVar(&query.Text, "text", "", "accessible text substring")
	findCmd.Flags().StringSliceVar(&query.States, "state", nil, "required accessible state (repeatable)")
	findCmd.Flags().StringArrayVar(&attributeFlags, "attribute", nil, "required attribute key=value (repeatable)")

	var outlineOpts accessibilityCLIOptions
	outline := &cobra.Command{Use: "outline", Short: "Print a compact semantic outline for an explicit scope", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		root, err := outlineOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		value, err := pf.Accessibility.Outline(c.Context(), root, outlineOpts.snapshot(), accessibility.OutlineOptions{MaxDepth: outlineOpts.maxDepth, MaxNodes: outlineOpts.maxNodes})
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), outlineOpts.format(), value)
	}}
	common(outline, &outlineOpts)

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
		return accessibilityOutput(c.OutOrStdout(), appOpts.format(), value)
	}}
	common(focused, &appOpts)

	var pointX, pointY int
	atPoint := &cobra.Command{Use: "at-point [X Y]", Short: "Inspect the accessible object at a screen coordinate", Args: func(_ *cobra.Command, args []string) error {
		switch len(args) {
		case 0:
			return nil
		case 2:
			x, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("at-point x: %w", err)
			}
			y, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("at-point y: %w", err)
			}
			pointX, pointY = x, y
			return nil
		default:
			return fmt.Errorf("at-point expects zero or two coordinates")
		}
	}, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		value, err := pf.Accessibility.AtPoint(c.Context(), pointX, pointY)
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), appOpts.format(), value)
	}}
	common(atPoint, &appOpts)
	atPoint.Flags().IntVar(&pointX, "x", 0, "screen x coordinate")
	atPoint.Flags().IntVar(&pointY, "y", 0, "screen y coordinate")

	var eventBuffer int
	events := &cobra.Command{Use: "events", Short: "Stream AT-SPI invalidation events as JSON lines", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		stream, err := pf.Accessibility.Events(c.Context(), accessibility.EventOptions{Buffer: eventBuffer})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(c.OutOrStdout())
		for {
			select {
			case <-c.Context().Done():
				return c.Context().Err()
			case event, ok := <-stream:
				if !ok {
					return nil
				}
				if err := enc.Encode(event); err != nil {
					return err
				}
			}
		}
	}}
	events.Flags().IntVar(&eventBuffer, "buffer", 0, "event buffer size")

	var actionOpts accessibilityCLIOptions
	var actionIndex int32
	var actionName string
	action := &cobra.Command{Use: "invoke-action", Short: "Invoke an AT-SPI action on an explicit node", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		node, err := actionOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		switch {
		case strings.TrimSpace(actionName) != "":
			err = pf.Accessibility.InvokeActionByName(c.Context(), node, actionName)
		case actionIndex >= 0:
			err = pf.Accessibility.InvokeAction(c.Context(), node, actionIndex)
		default:
			_, err = pf.Accessibility.InvokeDefaultAction(c.Context(), node)
		}
		return err
	}}
	common(action, &actionOpts)
	action.Flags().Int32Var(&actionIndex, "action-index", -1, "stable AT-SPI action index")
	action.Flags().StringVar(&actionName, "action-name", "", "exact AT-SPI action name (must be unique)")
	var focusOpts accessibilityCLIOptions
	focus := &cobra.Command{Use: "focus", Short: "Focus an explicit accessible node", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		node, err := focusOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		return pf.Accessibility.FocusNode(c.Context(), node)
	}}
	common(focus, &focusOpts)
	var scrollOpts accessibilityCLIOptions
	scroll := &cobra.Command{Use: "scroll", Short: "Scroll an explicit accessible node into view", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		node, err := scrollOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		return pf.Accessibility.ScrollNodeIntoView(c.Context(), node)
	}}
	common(scroll, &scrollOpts)
	var scrollPointOpts accessibilityCLIOptions
	var scrollPointX, scrollPointY int
	var scrollPointCoord string
	scrollPoint := &cobra.Command{Use: "scroll-to-point", Short: "Scroll an accessible node to a point in a coordinate space", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		node, err := scrollPointOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		coord, err := parseAccessibilityCoordType(scrollPointCoord)
		if err != nil {
			return err
		}
		return pf.Accessibility.ScrollToPoint(c.Context(), node, coord, scrollPointX, scrollPointY)
	}}
	common(scrollPoint, &scrollPointOpts)
	scrollPoint.Flags().IntVar(&scrollPointX, "x", 0, "point x coordinate")
	scrollPoint.Flags().IntVar(&scrollPointY, "y", 0, "point y coordinate")
	scrollPoint.Flags().StringVar(&scrollPointCoord, "coordinate-space", "screen", "coordinate space: screen, window, or parent")
	var positionOpts accessibilityCLIOptions
	var positionX, positionY int
	var positionCoord string
	setPosition := &cobra.Command{Use: "set-position", Short: "Move an accessible Component", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		node, err := positionOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		coord, err := parseAccessibilityCoordType(positionCoord)
		if err != nil {
			return err
		}
		return pf.Accessibility.SetPosition(c.Context(), node, positionX, positionY, coord)
	}}
	common(setPosition, &positionOpts)
	setPosition.Flags().IntVar(&positionX, "x", 0, "position x coordinate")
	setPosition.Flags().IntVar(&positionY, "y", 0, "position y coordinate")
	setPosition.Flags().StringVar(&positionCoord, "coordinate-space", "screen", "coordinate space: screen, window, or parent")
	var sizeOpts accessibilityCLIOptions
	var sizeWidth, sizeHeight int
	setSize := &cobra.Command{Use: "set-size", Short: "Resize an accessible Component", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		node, err := sizeOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		return pf.Accessibility.SetSize(c.Context(), node, sizeWidth, sizeHeight)
	}}
	common(setSize, &sizeOpts)
	setSize.Flags().IntVar(&sizeWidth, "width", 0, "component width")
	setSize.Flags().IntVar(&sizeHeight, "height", 0, "component height")
	var extentsOpts accessibilityCLIOptions
	var extentX, extentY, extentWidth, extentHeight int
	var extentCoord string
	setExtents := &cobra.Command{Use: "set-extents", Short: "Move and resize an accessible Component", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		node, err := extentsOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		coord, err := parseAccessibilityCoordType(extentCoord)
		if err != nil {
			return err
		}
		return pf.Accessibility.SetExtents(c.Context(), node, extentX, extentY, extentWidth, extentHeight, coord)
	}}
	common(setExtents, &extentsOpts)
	setExtents.Flags().IntVar(&extentX, "x", 0, "position x coordinate")
	setExtents.Flags().IntVar(&extentY, "y", 0, "position y coordinate")
	setExtents.Flags().IntVar(&extentWidth, "width", 0, "component width")
	setExtents.Flags().IntVar(&extentHeight, "height", 0, "component height")
	setExtents.Flags().StringVar(&extentCoord, "coordinate-space", "screen", "coordinate space: screen, window, or parent")
	var valueOpts accessibilityCLIOptions
	var value float64
	setValue := &cobra.Command{Use: "set-value", Short: "Set an accessible Value", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		node, err := valueOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		return pf.Accessibility.SetCurrentValue(c.Context(), node, value)
	}}
	common(setValue, &valueOpts)
	setValue.Flags().Float64Var(&value, "value", 0, "new current value")
	var textOpts accessibilityCLIOptions
	var textValue string
	setText := &cobra.Command{Use: "set-text", Short: "Set editable text through AT-SPI", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		node, err := textOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		return pf.Accessibility.ReplaceEditableText(c.Context(), node, textValue)
	}}
	common(setText, &textOpts)
	setText.Flags().StringVar(&textValue, "text", "", "replacement text")
	nodeMutation := func(use, short string, opts *accessibilityCLIOptions, mutate func(*perfuncted.Session, context.Context, accessibility.NodeID) error) *cobra.Command {
		command := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			pf, err := openPF(c.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			node, err := opts.root(c.Context(), pf)
			if err != nil {
				return err
			}
			return mutate(pf, c.Context(), node)
		}}
		common(command, opts)
		return command
	}
	var textRangeOpts accessibilityCLIOptions
	var textSelection int32
	var textStart, textEnd int32
	setTextSelection := nodeMutation("set-text-selection", "Set a range through AT-SPI Text", &textRangeOpts, func(pf *perfuncted.Session, ctx context.Context, node accessibility.NodeID) error {
		return pf.Accessibility.SetTextSelection(ctx, node, textSelection, textStart, textEnd)
	})
	setTextSelection.Flags().Int32Var(&textSelection, "selection", 0, "selection number")
	setTextSelection.Flags().Int32Var(&textStart, "start", 0, "selection start offset")
	setTextSelection.Flags().Int32Var(&textEnd, "end", 0, "selection end offset")
	var addTextSelectionOpts accessibilityCLIOptions
	addTextSelection := nodeMutation("add-text-selection", "Add a range through AT-SPI Text", &addTextSelectionOpts, func(pf *perfuncted.Session, ctx context.Context, node accessibility.NodeID) error {
		return pf.Accessibility.AddTextSelection(ctx, node, textStart, textEnd)
	})
	addTextSelection.Flags().Int32Var(&textStart, "start", 0, "selection start offset")
	addTextSelection.Flags().Int32Var(&textEnd, "end", 0, "selection end offset")
	var removeTextSelectionOpts accessibilityCLIOptions
	removeTextSelection := nodeMutation("remove-text-selection", "Remove a range through AT-SPI Text", &removeTextSelectionOpts, func(pf *perfuncted.Session, ctx context.Context, node accessibility.NodeID) error {
		return pf.Accessibility.RemoveTextSelection(ctx, node, textSelection)
	})
	removeTextSelection.Flags().Int32Var(&textSelection, "selection", 0, "selection number")
	var selectionOpts accessibilityCLIOptions
	var childIndex int32
	selectChild := &cobra.Command{Use: "select-child", Short: "Select a child through AT-SPI Selection", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		node, err := selectionOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		return pf.Accessibility.SelectChild(c.Context(), node, childIndex)
	}}
	common(selectChild, &selectionOpts)
	selectChild.Flags().Int32Var(&childIndex, "index", 0, "child index")
	var selectionAllOpts accessibilityCLIOptions
	selectAll := nodeMutation("select-all", "Select all children through AT-SPI Selection", &selectionAllOpts, func(pf *perfuncted.Session, ctx context.Context, node accessibility.NodeID) error {
		return pf.Accessibility.SelectAll(ctx, node)
	})
	var clearSelectionOpts accessibilityCLIOptions
	clearSelection := nodeMutation("clear-selection", "Clear AT-SPI Selection", &clearSelectionOpts, func(pf *perfuncted.Session, ctx context.Context, node accessibility.NodeID) error {
		return pf.Accessibility.ClearSelection(ctx, node)
	})
	var deselectAllOpts accessibilityCLIOptions
	deselectAll := nodeMutation("deselect-all", "Deselect all children through AT-SPI Selection", &deselectAllOpts, func(pf *perfuncted.Session, ctx context.Context, node accessibility.NodeID) error {
		return pf.Accessibility.DeselectAll(ctx, node)
	})
	var tableOpts accessibilityCLIOptions
	var tableIndex int32
	selectRow := nodeMutation("select-row", "Select a table row through AT-SPI Table", &tableOpts, func(pf *perfuncted.Session, ctx context.Context, node accessibility.NodeID) error {
		return pf.Accessibility.SelectRow(ctx, node, tableIndex)
	})
	selectRow.Flags().Int32Var(&tableIndex, "index", 0, "row index")
	var deselectRowOpts accessibilityCLIOptions
	deselectRow := nodeMutation("deselect-row", "Deselect a table row through AT-SPI Table", &deselectRowOpts, func(pf *perfuncted.Session, ctx context.Context, node accessibility.NodeID) error {
		return pf.Accessibility.DeselectRow(ctx, node, tableIndex)
	})
	deselectRow.Flags().Int32Var(&tableIndex, "index", 0, "row index")
	var selectColumnOpts accessibilityCLIOptions
	selectColumn := nodeMutation("select-column", "Select a table column through AT-SPI Table", &selectColumnOpts, func(pf *perfuncted.Session, ctx context.Context, node accessibility.NodeID) error {
		return pf.Accessibility.SelectColumn(ctx, node, tableIndex)
	})
	selectColumn.Flags().Int32Var(&tableIndex, "index", 0, "column index")
	var deselectColumnOpts accessibilityCLIOptions
	deselectColumn := nodeMutation("deselect-column", "Deselect a table column through AT-SPI Table", &deselectColumnOpts, func(pf *perfuncted.Session, ctx context.Context, node accessibility.NodeID) error {
		return pf.Accessibility.DeselectColumn(ctx, node, tableIndex)
	})
	deselectColumn.Flags().Int32Var(&tableIndex, "index", 0, "column index")
	reopen := &cobra.Command{Use: "reopen", Short: "Explicitly reopen the target accessibility bus", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openPF(c.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		return pf.Accessibility.ReopenAccessibility(c.Context())
	}}

	cmd.AddCommand(applications, snapshot, findCmd, outline, focused, atPoint, events, action, focus, scroll, scrollPoint, setPosition, setSize, setExtents, setValue, setText, setTextSelection, addTextSelection, removeTextSelection, selectChild, selectAll, clearSelection, deselectAll, selectRow, deselectRow, selectColumn, deselectColumn, reopen)
	return cmd
}

func parseAccessibilityCoordType(value string) (accessibility.CoordType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "screen":
		return accessibility.CoordTypeScreen, nil
	case "window":
		return accessibility.CoordTypeWindow, nil
	case "parent":
		return accessibility.CoordTypeParent, nil
	default:
		return 0, fmt.Errorf("unknown coordinate space %q (want screen, window, or parent)", value)
	}
}

func parseAccessibilityAttributes(values []string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	attributes := make(map[string]string, len(values))
	for _, value := range values {
		key, raw, ok := strings.Cut(value, "=")
		key, raw = strings.TrimSpace(key), strings.TrimSpace(raw)
		if !ok || key == "" {
			continue
		}
		attributes[key] = raw
	}
	return attributes
}
