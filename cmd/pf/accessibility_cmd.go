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
	cmd := &cobra.Command{Use: "a11y", Short: "Inspect and operate the AT-SPI accessibility tree"}

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

	rawOpts := &accessibilityRawOptions{}
	var rawOp string
	var rawActionName string
	var rawActionIndex int32
	var rawAlignment string
	var rawCoord string
	var rawX, rawY, rawWidth, rawHeight int
	var rawValue float64
	var rawText, rawRangeText, rawSelectionsJSON string
	var rawStart, rawEnd, rawOffset, rawSelection, rawPosition, rawIndex int32
	raw := &cobra.Command{Use: "raw", Short: "Invoke one typed AT-SPI primitive with an explicit handle", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilitySession(c.Context(), openPF)
		if err != nil {
			return err
		}
		defer pf.Close()
		op := strings.ToLower(strings.TrimSpace(rawOp))
		if op == "" {
			return fmt.Errorf("raw requires --op")
		}
		if op == "reopen" {
			if reopenErr := pf.Accessibility.Reopen(c.Context()); reopenErr != nil {
				return reopenErr
			}
			return accessibilityOutput(c.OutOrStdout(), rawOpts.json, "ok")
		}
		node, err := rawOpts.node()
		if err != nil {
			return err
		}
		inv := rawInvocation{op: op, actionName: rawActionName, actionIndex: rawActionIndex, alignment: rawAlignment, coord: rawCoord, x: rawX, y: rawY, width: rawWidth, height: rawHeight, value: rawValue, text: rawText, rangeText: rawRangeText, selectionsJSON: rawSelectionsJSON, start: rawStart, end: rawEnd, offset: rawOffset, selection: rawSelection, position: rawPosition, index: rawIndex}
		value, err := invokeRawOp(c.Context(), pf.Accessibility, node, inv)
		if err != nil {
			return err
		}
		if value == nil {
			value = "ok"
		}
		return accessibilityOutput(c.OutOrStdout(), rawOpts.json, value)
	}}
	addJSONFlag(raw, &rawOpts.json)
	raw.Flags().StringVar(&rawOp, "op", "", "primitive: action, focus, scroll, scroll-to-point, set-position, set-size, set-extents, set-value, set-text-contents, insert-text, delete-text, copy-text, cut-text, paste-text, set-caret, set-text-selection, add-text-selection, remove-text-selection, set-document-text-selections, select-child, deselect-child, select-all, clear-selection, deselect-selected-child, select-row, deselect-row, select-column, deselect-column, reopen")
	raw.Flags().StringVar(&rawOpts.bus, "bus", "", "AT-SPI object bus name")
	raw.Flags().StringVar(&rawOpts.path, "path", "", "AT-SPI object path")
	raw.Flags().Uint64Var(&rawOpts.generation, "generation", 0, "current accessibility generation")
	raw.Flags().Int32Var(&rawActionIndex, "action-index", -1, "stable AT-SPI action index")
	raw.Flags().StringVar(&rawActionName, "action-name", "", "exact AT-SPI action name")
	raw.Flags().StringVar(&rawAlignment, "alignment", "anywhere", "alignment for scroll")
	raw.Flags().StringVar(&rawCoord, "coordinate-space", "screen", "coordinate space: screen, window, or parent")
	raw.Flags().IntVar(&rawX, "x", 0, "x coordinate")
	raw.Flags().IntVar(&rawY, "y", 0, "y coordinate")
	raw.Flags().IntVar(&rawWidth, "width", 0, "component width")
	raw.Flags().IntVar(&rawHeight, "height", 0, "component height")
	raw.Flags().Float64Var(&rawValue, "value", 0, "new current value")
	raw.Flags().StringVar(&rawText, "text", "", "replacement text")
	raw.Flags().StringVar(&rawRangeText, "range-text", "", "range replacement text")
	raw.Flags().StringVar(&rawSelectionsJSON, "selections", "[]", "JSON array of DocumentTextSelection values")
	raw.Flags().Int32Var(&rawStart, "start", 0, "start character offset")
	raw.Flags().Int32Var(&rawEnd, "end", 0, "end character offset")
	raw.Flags().Int32Var(&rawOffset, "offset", 0, "character offset")
	raw.Flags().Int32Var(&rawSelection, "selection", 0, "selection number")
	raw.Flags().Int32Var(&rawPosition, "position", 0, "paste character position")
	raw.Flags().Int32Var(&rawIndex, "index", 0, "child/row/column index")

	cmd.AddCommand(apps, tree, find, focused, atPoint, events, action, focus, text, raw)
	return cmd
}

type rawInvocation struct {
	op              string
	actionName      string
	actionIndex     int32
	alignment       string
	coord           string
	x, y            int
	width, height   int
	value           float64
	text, rangeText string
	selectionsJSON  string
	start, end      int32
	offset          int32
	selection       int32
	position        int32
	index           int32
}

func invokeRawOp(ctx context.Context, bundle *perfuncted.AccessibilityBundle, node accessibility.NodeID, in rawInvocation) (any, error) { //nolint:gocyclo // one dispatch table over the typed primitive surface.
	switch in.op {
	case "action":
		if strings.TrimSpace(in.actionName) != "" {
			return bundle.InvokeActionByName(ctx, node, in.actionName)
		}
		if in.actionIndex >= 0 {
			return nil, bundle.InvokeAction(ctx, node, in.actionIndex)
		}
		return bundle.InvokeDefaultAction(ctx, node)
	case "focus":
		return nil, bundle.FocusNode(ctx, node)
	case "scroll":
		kind, err := parseAccessibilityScrollType(in.alignment)
		if err != nil {
			return nil, err
		}
		return nil, bundle.ScrollTo(ctx, node, kind)
	case "scroll-to-point":
		coord, err := parseAccessibilityCoordType(in.coord)
		if err != nil {
			return nil, err
		}
		return nil, bundle.ScrollToPoint(ctx, node, coord, in.x, in.y)
	case "set-position":
		coord, err := parseAccessibilityCoordType(in.coord)
		if err != nil {
			return nil, err
		}
		return nil, bundle.SetPosition(ctx, node, in.x, in.y, coord)
	case "set-size":
		return nil, bundle.SetSize(ctx, node, in.width, in.height)
	case "set-extents":
		coord, err := parseAccessibilityCoordType(in.coord)
		if err != nil {
			return nil, err
		}
		return nil, bundle.SetExtents(ctx, node, in.x, in.y, in.width, in.height, coord)
	case "set-value":
		return nil, bundle.SetValue(ctx, node, in.value)
	case "set-text-contents":
		return nil, bundle.ReplaceEditableText(ctx, node, in.text)
	case "insert-text":
		text := in.rangeText
		if text == "" {
			text = in.text
		}
		return nil, bundle.InsertText(ctx, node, in.offset, text)
	case "delete-text":
		return nil, bundle.DeleteText(ctx, node, in.start, in.end)
	case "copy-text":
		return nil, bundle.CopyText(ctx, node, in.start, in.end)
	case "cut-text":
		return nil, bundle.CutText(ctx, node, in.start, in.end)
	case "paste-text":
		return nil, bundle.PasteText(ctx, node, in.position)
	case "set-caret":
		return nil, bundle.SetCaretOffset(ctx, node, in.offset)
	case "set-text-selection":
		return nil, bundle.SetTextSelection(ctx, node, in.selection, in.start, in.end)
	case "add-text-selection":
		return nil, bundle.AddTextSelection(ctx, node, in.start, in.end)
	case "remove-text-selection":
		return nil, bundle.RemoveTextSelection(ctx, node, in.selection)
	case "set-document-text-selections":
		var selections []accessibility.DocumentTextSelection
		if err := json.Unmarshal([]byte(in.selectionsJSON), &selections); err != nil {
			return nil, fmt.Errorf("invalid --selections JSON: %w", err)
		}
		return nil, bundle.SetTextSelections(ctx, node, selections)
	case "select-child":
		return nil, bundle.SelectChild(ctx, node, in.index)
	case "deselect-child":
		return nil, bundle.DeselectChild(ctx, node, in.index)
	case "select-all":
		return nil, bundle.SelectAll(ctx, node)
	case "clear-selection":
		return nil, bundle.ClearSelection(ctx, node)
	case "deselect-selected-child":
		return nil, bundle.DeselectSelectedChild(ctx, node)
	case "select-row":
		return nil, bundle.SelectRow(ctx, node, in.index)
	case "deselect-row":
		return nil, bundle.DeselectRow(ctx, node, in.index)
	case "select-column":
		return nil, bundle.SelectColumn(ctx, node, in.index)
	case "deselect-column":
		return nil, bundle.DeselectColumn(ctx, node, in.index)
	default:
		return nil, fmt.Errorf("unknown raw op %q", in.op)
	}
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
