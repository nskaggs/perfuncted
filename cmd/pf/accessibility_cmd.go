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

// accessibilityCLIOptions contains only selectors and bounds used by the
// workflow commands. Protocol handles belong under raw, where they are
// intentionally explicit and never confused with semantic selectors.
type accessibilityCLIOptions struct {
	json           bool
	appName        string
	pid            int32
	window         string
	windowID       string
	maxDepth       int
	maxNodes       int
	maxTextBytes   int
	maxTotalBytes  int
	visibleOnly    bool
	allowSensitive bool
}

func (o accessibilityCLIOptions) root(ctx context.Context, pf *perfuncted.Session) (accessibility.NodeID, error) {
	if strings.TrimSpace(o.appName) == "" && o.pid == 0 && strings.TrimSpace(o.window) == "" && strings.TrimSpace(o.windowID) == "" {
		return accessibility.NodeID{}, fmt.Errorf("accessibility: an explicit --app, --pid, --window, or --window-id scope is required")
	}
	if strings.TrimSpace(o.window) != "" || strings.TrimSpace(o.windowID) != "" {
		return o.windowRoot(ctx, pf)
	}
	app, err := pf.Accessibility.FindApplication(ctx, accessibility.ApplicationFilter{
		Name:        strings.TrimSpace(o.appName),
		PID:         o.pid,
		WindowID:    strings.TrimSpace(o.windowID),
		WindowTitle: strings.TrimSpace(o.window),
	})
	if err != nil {
		return accessibility.NodeID{}, err
	}
	return app.ID, nil
}

func (o accessibilityCLIOptions) windowRoot(ctx context.Context, pf *perfuncted.Session) (accessibility.NodeID, error) {
	managedWindowID := strings.TrimSpace(o.windowID)
	if strings.TrimSpace(o.window) != "" {
		managedWindow, err := pf.Windows.Find(ctx, perfuncted.WindowMatch{TitleExact: strings.TrimSpace(o.window)})
		if err != nil {
			return accessibility.NodeID{}, err
		}
		if managedWindowID != "" && managedWindow.ID().String() != managedWindowID {
			return accessibility.NodeID{}, fmt.Errorf("accessibility: --window and --window-id identify different managed windows")
		}
		managedWindowID = managedWindow.ID().String()
	}
	// Preserve additional app/PID scope constraints even though the returned
	// root is the selected window rather than its application.
	if strings.TrimSpace(o.appName) != "" || o.pid != 0 {
		if _, err := pf.Accessibility.FindApplication(ctx, accessibility.ApplicationFilter{
			Name:     strings.TrimSpace(o.appName),
			PID:      o.pid,
			WindowID: managedWindowID,
		}); err != nil {
			return accessibility.NodeID{}, err
		}
	}
	scope, err := pf.Accessibility.WindowRoot(ctx, managedWindowID)
	if err != nil {
		return accessibility.NodeID{}, err
	}
	return scope.Root, nil
}

func (o accessibilityCLIOptions) snapshot() accessibility.SnapshotOptions {
	return accessibility.SnapshotOptions{
		MaxDepth:       o.maxDepth,
		MaxNodes:       o.maxNodes,
		MaxTextBytes:   o.maxTextBytes,
		MaxTotalBytes:  o.maxTotalBytes,
		VisibleOnly:    o.visibleOnly,
		AllowSensitive: o.allowSensitive,
	}
}

type accessibilityRawOptions struct {
	json       bool
	bus        string
	path       string
	generation uint64
}

func (o accessibilityRawOptions) node() (accessibility.NodeID, error) {
	if strings.TrimSpace(o.bus) == "" || strings.TrimSpace(o.path) == "" {
		return accessibility.NodeID{}, fmt.Errorf("raw node operation requires both --bus and --path")
	}
	if o.generation == 0 {
		return accessibility.NodeID{}, fmt.Errorf("raw node operation requires --generation from a current snapshot")
	}
	return accessibility.NodeID{BusName: strings.TrimSpace(o.bus), ObjectPath: strings.TrimSpace(o.path), Generation: o.generation}, nil
}

func accessibilityOutput(w io.Writer, jsonMode bool, value any) error {
	if jsonMode {
		return json.NewEncoder(w).Encode(value)
	}
	return writeAccessibilityText(w, value)
}

func writeAccessibilityText(w io.Writer, value any) error {
	switch typed := value.(type) {
	case []accessibility.Application:
		for _, app := range typed {
			if _, err := fmt.Fprintf(w, "%s\tpid=%d\t%s/%s@%d\n", app.Name, app.PID, app.ID.BusName, app.ID.ObjectPath, app.ID.Generation); err != nil {
				return err
			}
		}
		return nil
	case accessibility.Node:
		return writeAccessibilityNode(w, typed, "")
	case []accessibility.Node:
		for _, node := range typed {
			if err := writeAccessibilityNode(w, node, ""); err != nil {
				return err
			}
		}
		return nil
	case accessibility.Snapshot:
		return writeAccessibilityText(w, accessibility.BuildOutline(typed, accessibility.OutlineOptions{}))
	case accessibility.Outline:
		if err := writeAccessibilityOutlineNode(w, typed.Root, ""); err != nil {
			return err
		}
		if typed.Truncated {
			_, err := fmt.Fprintf(w, "[truncated generation=%d warnings=%v]\n", typed.Generation, typed.Warnings)
			return err
		}
		return nil
	case accessibility.Action:
		_, err := fmt.Fprintf(w, "action index=%d name=%q description=%q key=%q\n", typed.Index, typed.Name, typed.Description, typed.KeyBinding)
		return err
	case perfuncted.AccessibilityActionReceipt:
		if err := writeAccessibilityNode(w, typed.Node, "target "); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "action index=%d name=%q mechanism=%s generation=%d\n", typed.Action.Index, typed.Action.Name, typed.Mechanism, typed.Generation)
		return err
	default:
		_, err := fmt.Fprintln(w, value)
		return err
	}
}

func writeAccessibilityNode(w io.Writer, node accessibility.Node, prefix string) error {
	_, err := fmt.Fprintf(w, "%s%s %q [%s/%s@%d]\n", prefix, node.Role, node.Name, node.ID.BusName, node.ID.ObjectPath, node.ID.Generation)
	return err
}

func writeAccessibilityOutlineNode(w io.Writer, node accessibility.OutlineNode, indent string) error {
	label := node.Name
	if label == "" {
		label = node.Text
	}
	if _, err := fmt.Fprintf(w, "%s%s %q [%s/%s@%d]\n", indent, node.Role, label, node.ID.BusName, node.ID.ObjectPath, node.ID.Generation); err != nil {
		return err
	}
	for _, child := range node.Children {
		if err := writeAccessibilityOutlineNode(w, child, indent+"  "); err != nil {
			return err
		}
	}
	return nil
}

func addJSONFlag(cmd *cobra.Command, target *bool) {
	cmd.Flags().BoolVar(target, "json", false, "write machine-readable JSON")
}

func addScopeFlags(cmd *cobra.Command, opts *accessibilityCLIOptions) {
	cmd.Flags().StringVar(&opts.appName, "app", "", "accessible application name substring")
	cmd.Flags().Int32Var(&opts.pid, "pid", 0, "exact application process ID")
	cmd.Flags().StringVar(&opts.window, "window", "", "exact managed window title")
	cmd.Flags().StringVar(&opts.windowID, "window-id", "", "managed window ID")
}

func addSnapshotFlags(cmd *cobra.Command, opts *accessibilityCLIOptions) {
	cmd.Flags().IntVar(&opts.maxDepth, "max-depth", 0, "maximum tree depth")
	cmd.Flags().IntVar(&opts.maxNodes, "max-nodes", 0, "maximum nodes")
	cmd.Flags().IntVar(&opts.maxTextBytes, "max-text-bytes", 0, "maximum UTF-8 text bytes per node")
	cmd.Flags().IntVar(&opts.maxTotalBytes, "max-total-bytes", 0, "hard maximum serialized snapshot bytes")
	cmd.Flags().BoolVar(&opts.visibleOnly, "visible-only", false, "exclude invisible/off-screen nodes")
	cmd.Flags().BoolVar(&opts.allowSensitive, "allow-sensitive", false, "include protected text and values")
}

func addQueryFlags(cmd *cobra.Command, query *accessibility.Query, attributes *[]string) {
	cmd.Flags().StringVar(&query.Name, "name", "", "accessible name substring")
	cmd.Flags().StringVar(&query.Role, "role", "", "accessible role substring")
	cmd.Flags().StringVar(&query.Text, "text", "", "accessible text substring")
	cmd.Flags().StringArrayVar(&query.States, "state", nil, "required accessible state (repeatable)")
	cmd.Flags().StringArrayVar(attributes, "attribute", nil, "required attribute key=value (repeatable)")
}

func openAccessibilitySession(ctx context.Context, openPF sessionOpener) (*perfuncted.Session, error) {
	pf, err := openPF(ctx)
	if err != nil {
		return nil, err
	}
	if pf == nil || pf.Accessibility == nil {
		if pf != nil {
			_ = pf.Close() //nolint:contextcheck // close is the bounded cleanup for a rejected session.
		}
		return nil, fmt.Errorf("accessibility: capability unavailable")
	}
	return pf, nil
}

func openAccessibilityScopeSession(ctx context.Context, openPF, openWindowPF sessionOpener, opts accessibilityCLIOptions) (*perfuncted.Session, error) {
	if opts.hasWindowScope() && openWindowPF != nil {
		return openAccessibilitySession(ctx, openWindowPF)
	}
	return openAccessibilitySession(ctx, openPF)
}

func (o accessibilityCLIOptions) hasWindowScope() bool {
	return strings.TrimSpace(o.window) != "" || strings.TrimSpace(o.windowID) != ""
}

func accessibilityCmd(openPF, openWindowPF sessionOpener) *cobra.Command { //nolint:gocyclo // command wiring keeps the public workflow in one place.
	cmd := &cobra.Command{Use: "a11y", Aliases: []string{"accessibility"}, Short: "Inspect and operate the AT-SPI accessibility tree"}

	var appsJSON bool
	apps := &cobra.Command{Use: "apps", Short: "List registered accessible applications", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilitySession(c.Context(), openPF)
		if err != nil {
			return err
		}
		defer pf.Close()
		value, err := pf.Accessibility.Applications(c.Context())
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), appsJSON, value)
	}}
	addJSONFlag(apps, &appsJSON)

	var treeOpts accessibilityCLIOptions
	tree := &cobra.Command{Use: "tree", Short: "Capture a bounded tree for one application or managed window", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilityScopeSession(c.Context(), openPF, openWindowPF, treeOpts)
		if err != nil {
			return err
		}
		defer pf.Close()
		root, err := treeOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		snapshot, err := pf.Accessibility.Snapshot(c.Context(), root, treeOpts.snapshot())
		if err != nil {
			return err
		}
		if treeOpts.json {
			return accessibilityOutput(c.OutOrStdout(), true, snapshot)
		}
		return accessibilityOutput(c.OutOrStdout(), false, accessibility.BuildOutline(snapshot, accessibility.OutlineOptions{MaxDepth: treeOpts.maxDepth, MaxNodes: treeOpts.maxNodes}))
	}}
	addJSONFlag(tree, &treeOpts.json)
	addScopeFlags(tree, &treeOpts)
	addSnapshotFlags(tree, &treeOpts)

	var findOpts accessibilityCLIOptions
	var findQuery accessibility.Query
	var findAttributes []string
	find := &cobra.Command{Use: "find", Short: "Find semantic nodes in one application or managed window", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilityScopeSession(c.Context(), openPF, openWindowPF, findOpts)
		if err != nil {
			return err
		}
		defer pf.Close()
		root, err := findOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		findQuery.Attributes = parseAccessibilityAttributes(findAttributes)
		value, err := pf.Accessibility.Find(c.Context(), root, findQuery, findOpts.snapshot())
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), findOpts.json, value)
	}}
	addJSONFlag(find, &findOpts.json)
	addScopeFlags(find, &findOpts)
	addSnapshotFlags(find, &findOpts)
	addQueryFlags(find, &findQuery, &findAttributes)

	var focusedJSON bool
	focused := &cobra.Command{Use: "focused", Short: "Show the currently focused accessible node", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilitySession(c.Context(), openPF)
		if err != nil {
			return err
		}
		defer pf.Close()
		value, err := pf.Accessibility.Focused(c.Context(), accessibility.SnapshotOptions{})
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), focusedJSON, value)
	}}
	addJSONFlag(focused, &focusedJSON)

	var pointJSON bool
	var pointX, pointY int
	atPoint := &cobra.Command{Use: "at-point [X Y]", Short: "Show the accessible object at screen coordinates", Args: func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		if len(args) != 2 {
			return fmt.Errorf("at-point expects zero or two coordinates")
		}
		var err error
		if pointX, err = strconv.Atoi(args[0]); err != nil {
			return fmt.Errorf("at-point x: %w", err)
		}
		if pointY, err = strconv.Atoi(args[1]); err != nil {
			return fmt.Errorf("at-point y: %w", err)
		}
		return nil
	}, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilitySession(c.Context(), openPF)
		if err != nil {
			return err
		}
		defer pf.Close()
		value, err := pf.Accessibility.AtPoint(c.Context(), pointX, pointY)
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), pointJSON, value)
	}}
	addJSONFlag(atPoint, &pointJSON)
	atPoint.Flags().IntVar(&pointX, "x", 0, "screen x coordinate")
	atPoint.Flags().IntVar(&pointY, "y", 0, "screen y coordinate")

	var eventBuffer int
	events := &cobra.Command{Use: "events", Short: "Stream bounded AT-SPI invalidation events", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilitySession(c.Context(), openPF)
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
	var actionQuery accessibility.Query
	var actionAttributes []string
	var actionName string
	action := &cobra.Command{Use: "action", Short: "Invoke one uniquely resolved semantic AT-SPI action", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilityScopeSession(c.Context(), openPF, openWindowPF, actionOpts)
		if err != nil {
			return err
		}
		defer pf.Close()
		root, err := actionOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		actionQuery.Attributes = parseAccessibilityAttributes(actionAttributes)
		receipt, err := pf.Accessibility.InvokeSemanticAction(c.Context(), root, actionQuery, actionName, actionOpts.snapshot())
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), actionOpts.json, receipt)
	}}
	addJSONFlag(action, &actionOpts.json)
	addScopeFlags(action, &actionOpts)
	addSnapshotFlags(action, &actionOpts)
	addQueryFlags(action, &actionQuery, &actionAttributes)
	action.Flags().StringVar(&actionName, "action", "", "exact action name; empty selects the provider's first action")
	action.Flags().StringVar(&actionName, "action-name", "", "alias for --action")

	var focusOpts accessibilityCLIOptions
	var focusQuery accessibility.Query
	var focusAttributes []string
	focus := &cobra.Command{Use: "focus", Short: "Resolve one semantic node and request AT-SPI focus", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilityScopeSession(c.Context(), openPF, openWindowPF, focusOpts)
		if err != nil {
			return err
		}
		defer pf.Close()
		root, err := focusOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		focusQuery.Attributes = parseAccessibilityAttributes(focusAttributes)
		node, err := pf.Accessibility.FindOne(c.Context(), root, focusQuery, focusOpts.snapshot())
		if err != nil {
			return err
		}
		if err := pf.Accessibility.FocusNode(c.Context(), node.ID); err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), focusOpts.json, node)
	}}
	addJSONFlag(focus, &focusOpts.json)
	addScopeFlags(focus, &focusOpts)
	addSnapshotFlags(focus, &focusOpts)
	addQueryFlags(focus, &focusQuery, &focusAttributes)

	var textOpts accessibilityCLIOptions
	var textQuery accessibility.Query
	var textAttributes []string
	var textValue string
	text := &cobra.Command{Use: "text", Short: "Resolve one editable semantic node and replace its text", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilityScopeSession(c.Context(), openPF, openWindowPF, textOpts)
		if err != nil {
			return err
		}
		defer pf.Close()
		root, err := textOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		textQuery.Attributes = parseAccessibilityAttributes(textAttributes)
		node, err := pf.Accessibility.FindOne(c.Context(), root, textQuery, textOpts.snapshot())
		if err != nil {
			return err
		}
		if err := pf.Accessibility.ReplaceEditableText(c.Context(), node.ID, textValue); err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), textOpts.json, node)
	}}
	addJSONFlag(text, &textOpts.json)
	addScopeFlags(text, &textOpts)
	addSnapshotFlags(text, &textOpts)
	addQueryFlags(text, &textQuery, &textAttributes)
	text.Flags().StringVar(&textValue, "value", "", "replacement text")

	raw := &cobra.Command{Use: "raw", Short: "Use typed AT-SPI protocol primitives with explicit handles"}
	addRawNode := func(use, short string, run func(accessibility.Automation, context.Context, accessibility.NodeID) (any, error)) *cobra.Command {
		opts := &accessibilityRawOptions{}
		command := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			pf, err := openAccessibilitySession(c.Context(), openPF)
			if err != nil {
				return err
			}
			defer pf.Close()
			node, err := opts.node()
			if err != nil {
				return err
			}
			automation, err := pf.Accessibility.RawAutomation()
			if err != nil {
				return err
			}
			value, err := run(automation, c.Context(), node)
			if err != nil {
				return err
			}
			if value == nil {
				value = "ok"
			}
			return accessibilityOutput(c.OutOrStdout(), opts.json, value)
		}}
		addJSONFlag(command, &opts.json)
		command.Flags().StringVar(&opts.bus, "bus", "", "AT-SPI object bus name")
		command.Flags().StringVar(&opts.path, "path", "", "AT-SPI object path")
		command.Flags().Uint64Var(&opts.generation, "generation", 0, "current accessibility generation")
		raw.AddCommand(command)
		return command
	}

	var rawActionName string
	var rawActionIndex int32
	rawAction := addRawNode("action", "Invoke an explicit AT-SPI action index or name", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		if strings.TrimSpace(rawActionName) != "" {
			return nil, automation.InvokeActionByName(ctx, node, rawActionName)
		}
		if rawActionIndex >= 0 {
			return nil, automation.InvokeAction(ctx, node, rawActionIndex)
		}
		return automation.InvokeDefaultAction(ctx, node)
	})
	rawAction.Flags().Int32Var(&rawActionIndex, "action-index", -1, "stable AT-SPI action index")
	rawAction.Flags().StringVar(&rawActionName, "action-name", "", "exact AT-SPI action name")
	addRawNode("focus", "Invoke the low-level AT-SPI Component GrabFocus primitive", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.GrabFocus(ctx, node)
	})
	var scrollType string
	rawScroll := addRawNode("scroll", "Invoke the low-level AT-SPI Component ScrollTo primitive", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		kind, err := parseAccessibilityScrollType(scrollType)
		if err != nil {
			return nil, err
		}
		return nil, automation.ScrollTo(ctx, node, kind)
	})
	rawScroll.Flags().StringVar(&scrollType, "alignment", "anywhere", "alignment: top-left, bottom-right, top-edge, bottom-edge, left-edge, right-edge, or anywhere")

	var pointCoord string
	var rawPointX, rawPointY int
	rawPoint := addRawNode("scroll-to-point", "Invoke Component ScrollToPoint", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		coord, err := parseAccessibilityCoordType(pointCoord)
		if err != nil {
			return nil, err
		}
		return nil, automation.ScrollToPoint(ctx, node, coord, rawPointX, rawPointY)
	})
	rawPoint.Flags().IntVar(&rawPointX, "x", 0, "point x coordinate")
	rawPoint.Flags().IntVar(&rawPointY, "y", 0, "point y coordinate")
	rawPoint.Flags().StringVar(&pointCoord, "coordinate-space", "screen", "coordinate space: screen, window, or parent")

	var rawX, rawY, rawWidth, rawHeight int
	var rawCoord string
	rawPosition := addRawNode("set-position", "Invoke Component SetPosition", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		coord, err := parseAccessibilityCoordType(rawCoord)
		if err != nil {
			return nil, err
		}
		return nil, automation.SetPosition(ctx, node, rawX, rawY, coord)
	})
	rawPosition.Flags().IntVar(&rawX, "x", 0, "position x coordinate")
	rawPosition.Flags().IntVar(&rawY, "y", 0, "position y coordinate")
	rawPosition.Flags().StringVar(&rawCoord, "coordinate-space", "screen", "coordinate space: screen, window, or parent")
	rawSize := addRawNode("set-size", "Invoke Component SetSize", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.SetSize(ctx, node, rawWidth, rawHeight)
	})
	rawSize.Flags().IntVar(&rawWidth, "width", 0, "component width")
	rawSize.Flags().IntVar(&rawHeight, "height", 0, "component height")
	rawExtents := addRawNode("set-extents", "Invoke Component SetExtents", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		coord, err := parseAccessibilityCoordType(rawCoord)
		if err != nil {
			return nil, err
		}
		return nil, automation.SetExtents(ctx, node, rawX, rawY, rawWidth, rawHeight, coord)
	})
	rawExtents.Flags().IntVar(&rawX, "x", 0, "position x coordinate")
	rawExtents.Flags().IntVar(&rawY, "y", 0, "position y coordinate")
	rawExtents.Flags().IntVar(&rawWidth, "width", 0, "component width")
	rawExtents.Flags().IntVar(&rawHeight, "height", 0, "component height")
	rawExtents.Flags().StringVar(&rawCoord, "coordinate-space", "screen", "coordinate space: screen, window, or parent")

	var rawValue float64
	rawCurrentValue := addRawNode("set-current-value", "Invoke Value SetCurrentValue", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.SetCurrentValue(ctx, node, rawValue)
	})
	rawCurrentValue.Flags().Float64Var(&rawValue, "value", 0, "new current value")
	rawValueCommand := addRawNode("set-value", "Invoke the typed Value SetValue primitive", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.SetValue(ctx, node, rawValue)
	})
	rawValueCommand.Flags().Float64Var(&rawValue, "value", 0, "new current value")
	var rawText string
	rawTextCommand := addRawNode("set-text-contents", "Invoke EditableText SetTextContents", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.SetTextContents(ctx, node, rawText)
	})
	rawTextCommand.Flags().StringVar(&rawText, "text", "", "replacement text")
	var rawStart, rawEnd, rawOffset int32
	var rawRangeText string
	rawReplace := addRawNode("replace-text", "Invoke EditableText ReplaceText", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.ReplaceText(ctx, node, rawStart, rawEnd, rawRangeText)
	})
	rawReplace.Flags().Int32Var(&rawStart, "start", 0, "start character offset")
	rawReplace.Flags().Int32Var(&rawEnd, "end", 0, "end character offset")
	rawReplace.Flags().StringVar(&rawRangeText, "text", "", "replacement text")
	rawInsert := addRawNode("insert-text", "Invoke EditableText InsertText", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.InsertText(ctx, node, rawOffset, rawRangeText)
	})
	rawInsert.Flags().Int32Var(&rawOffset, "offset", 0, "character offset")
	rawInsert.Flags().StringVar(&rawRangeText, "text", "", "inserted text")
	rawDelete := addRawNode("delete-text", "Invoke EditableText DeleteText", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.DeleteText(ctx, node, rawStart, rawEnd)
	})
	rawDelete.Flags().Int32Var(&rawStart, "start", 0, "start character offset")
	rawDelete.Flags().Int32Var(&rawEnd, "end", 0, "end character offset")
	rawCopy := addRawNode("copy-text", "Invoke Text CopyText", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.CopyText(ctx, node, rawStart, rawEnd)
	})
	rawCopy.Flags().Int32Var(&rawStart, "start", 0, "start character offset")
	rawCopy.Flags().Int32Var(&rawEnd, "end", 0, "end character offset")
	rawCut := addRawNode("cut-text", "Invoke Text CutText", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.CutText(ctx, node, rawStart, rawEnd)
	})
	rawCut.Flags().Int32Var(&rawStart, "start", 0, "start character offset")
	rawCut.Flags().Int32Var(&rawEnd, "end", 0, "end character offset")
	var pastePosition int32
	rawPaste := addRawNode("paste-text", "Invoke Text PasteText", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.PasteText(ctx, node, pastePosition)
	})
	rawPaste.Flags().Int32Var(&pastePosition, "position", 0, "paste character position")
	rawCaret := addRawNode("set-caret", "Invoke Text SetCaretOffset", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.SetCaretOffset(ctx, node, rawOffset)
	})
	rawCaret.Flags().Int32Var(&rawOffset, "offset", 0, "caret character offset")

	var selection int32
	var selectionStart, selectionEnd int32
	rawSetSelection := addRawNode("set-text-selection", "Invoke Text SetSelection", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.SetTextSelection(ctx, node, selection, selectionStart, selectionEnd)
	})
	rawSetSelection.Flags().Int32Var(&selection, "selection", 0, "selection number")
	rawSetSelection.Flags().Int32Var(&selectionStart, "start", 0, "start character offset")
	rawSetSelection.Flags().Int32Var(&selectionEnd, "end", 0, "end character offset")
	rawAddSelection := addRawNode("add-text-selection", "Invoke Text AddSelection", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.AddTextSelection(ctx, node, selectionStart, selectionEnd)
	})
	rawAddSelection.Flags().Int32Var(&selectionStart, "start", 0, "start character offset")
	rawAddSelection.Flags().Int32Var(&selectionEnd, "end", 0, "end character offset")
	rawRemoveSelection := addRawNode("remove-text-selection", "Invoke Text RemoveSelection", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.RemoveTextSelection(ctx, node, selection)
	})
	rawRemoveSelection.Flags().Int32Var(&selection, "selection", 0, "selection number")
	var documentSelectionsJSON string
	rawDocumentSelections := addRawNode("set-document-text-selections", "Invoke Document SetTextSelections", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		var selections []accessibility.DocumentTextSelection
		if err := json.Unmarshal([]byte(documentSelectionsJSON), &selections); err != nil {
			return nil, fmt.Errorf("invalid --selections JSON: %w", err)
		}
		return nil, automation.SetTextSelections(ctx, node, selections)
	})
	rawDocumentSelections.Flags().StringVar(&documentSelectionsJSON, "selections", "[]", "JSON array of DocumentTextSelection values")

	var childIndex int32
	rawSelectChild := addRawNode("select-child", "Invoke Selection SelectChild", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.SelectChild(ctx, node, childIndex)
	})
	rawSelectChild.Flags().Int32Var(&childIndex, "index", 0, "child index")
	rawDeselectChild := addRawNode("deselect-child", "Invoke Selection DeselectChild", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.DeselectChild(ctx, node, childIndex)
	})
	rawDeselectChild.Flags().Int32Var(&childIndex, "index", 0, "child index")
	addRawNode("select-all", "Invoke Selection SelectAll", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.SelectAll(ctx, node)
	})
	addRawNode("clear-selection", "Invoke Selection ClearSelection", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.ClearSelection(ctx, node)
	})
	addRawNode("deselect-all", "Invoke Selection DeselectAll", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.DeselectAll(ctx, node)
	})
	addRawNode("deselect-selected-child", "Invoke Selection DeselectSelectedChild", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.DeselectSelectedChild(ctx, node)
	})

	var tableIndex int32
	rawSelectRow := addRawNode("select-row", "Invoke Table SelectRow", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.SelectRow(ctx, node, tableIndex)
	})
	rawSelectRow.Flags().Int32Var(&tableIndex, "index", 0, "row index")
	rawDeselectRow := addRawNode("deselect-row", "Invoke Table DeselectRow", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.DeselectRow(ctx, node, tableIndex)
	})
	rawDeselectRow.Flags().Int32Var(&tableIndex, "index", 0, "row index")
	rawSelectColumn := addRawNode("select-column", "Invoke Table SelectColumn", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.SelectColumn(ctx, node, tableIndex)
	})
	rawSelectColumn.Flags().Int32Var(&tableIndex, "index", 0, "column index")
	rawDeselectColumn := addRawNode("deselect-column", "Invoke Table DeselectColumn", func(automation accessibility.Automation, ctx context.Context, node accessibility.NodeID) (any, error) {
		return nil, automation.DeselectColumn(ctx, node, tableIndex)
	})
	rawDeselectColumn.Flags().Int32Var(&tableIndex, "index", 0, "column index")

	rawReopen := &cobra.Command{Use: "reopen", Short: "Explicitly reopen the target accessibility bus", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilitySession(c.Context(), openPF)
		if err != nil {
			return err
		}
		defer pf.Close()
		if err := pf.Accessibility.Reopen(c.Context()); err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), false, "ok")
	}}
	raw.AddCommand(rawReopen)

	cmd.AddCommand(apps, tree, find, focused, atPoint, events, action, focus, text, raw)
	return cmd
}

func parseAccessibilityScrollType(value string) (accessibility.ScrollType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "top-left":
		return accessibility.ScrollTopLeft, nil
	case "bottom-right":
		return accessibility.ScrollBottomRight, nil
	case "top-edge":
		return accessibility.ScrollTopEdge, nil
	case "bottom-edge":
		return accessibility.ScrollBottomEdge, nil
	case "left-edge":
		return accessibility.ScrollLeftEdge, nil
	case "right-edge":
		return accessibility.ScrollRightEdge, nil
	case "anywhere", "any-where":
		return accessibility.ScrollAnyWhere, nil
	default:
		return 0, fmt.Errorf("unknown alignment %q", value)
	}
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
