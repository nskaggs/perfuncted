package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/window"
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

// requireScope enforces the local semantic selector before any AT-SPI session
// authorization. An incomplete query must never prompt for desktop access.
func (o accessibilityCLIOptions) requireScope() error {
	if o.pid < 0 {
		return fmt.Errorf("accessibility: --pid must be a nonnegative process ID")
	}
	for _, bound := range []struct {
		name  string
		value int
	}{
		{"--max-depth", o.maxDepth},
		{"--max-nodes", o.maxNodes},
		{"--max-text-bytes", o.maxTextBytes},
		{"--max-total-bytes", o.maxTotalBytes},
	} {
		if bound.value < 0 {
			return fmt.Errorf("accessibility: %s must be nonnegative", bound.name)
		}
	}
	if strings.TrimSpace(o.appName) == "" && o.pid == 0 && strings.TrimSpace(o.window) == "" && strings.TrimSpace(o.windowID) == "" {
		return fmt.Errorf("accessibility: an explicit --app, --pid, --window, or --window-id scope is required")
	}
	return nil
}

func (o accessibilityCLIOptions) root(ctx context.Context, pf *perfuncted.Session) (accessibility.NodeID, error) {
	if err := o.requireScope(); err != nil {
		return accessibility.NodeID{}, err
	}
	if strings.TrimSpace(o.window) != "" || strings.TrimSpace(o.windowID) != "" {
		return o.windowRoot(ctx, pf)
	}
	app, err := pf.Accessibility().WithContext(ctx).FindApplication(accessibility.ApplicationFilter{
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
	managedWindowID, err := o.managedWindowID(ctx, pf)
	if err != nil {
		return accessibility.NodeID{}, err
	}
	// Preserve additional app/PID scope constraints even though the returned
	// root is the selected window rather than its application.
	if strings.TrimSpace(o.appName) != "" || o.pid != 0 {
		if _, scopeErr := pf.Accessibility().WithContext(ctx).FindApplication(accessibility.ApplicationFilter{
			Name:     strings.TrimSpace(o.appName),
			PID:      o.pid,
			WindowID: managedWindowID,
		}); scopeErr != nil {
			return accessibility.NodeID{}, scopeErr
		}
	}
	scope, err := pf.Accessibility().WithContext(ctx).WindowRoot(managedWindowID)
	if err != nil {
		return accessibility.NodeID{}, err
	}
	return scope.Root, nil
}

func (o accessibilityCLIOptions) managedWindowID(ctx context.Context, pf *perfuncted.Session) (string, error) {
	managedWindowID := strings.TrimSpace(o.windowID)
	if strings.TrimSpace(o.window) != "" {
		managedWindow, err := pf.Windows().WithContext(ctx).Find(window.Match{TitleExact: strings.TrimSpace(o.window)})
		if err != nil {
			return "", err
		}
		if managedWindowID != "" && managedWindow.ID().String() != managedWindowID {
			return "", fmt.Errorf("accessibility: --window and --window-id identify different managed windows")
		}
		managedWindowID = managedWindow.ID().String()
	}
	if managedWindowID == "" {
		return "", fmt.Errorf("accessibility: a managed window ID is required")
	}
	return managedWindowID, nil
}

func (o accessibilityCLIOptions) locator(ctx context.Context, pf *perfuncted.Session, selector accessibility.Selector) (*perfuncted.AccessibilityLocator, error) {
	if o.hasWindowScope() {
		managedWindowID, err := o.managedWindowID(ctx, pf)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(o.appName) != "" || o.pid != 0 {
			if _, err := pf.Accessibility().WithContext(ctx).FindApplication(accessibility.ApplicationFilter{
				Name: strings.TrimSpace(o.appName), PID: o.pid, WindowID: managedWindowID,
			}); err != nil {
				return nil, err
			}
		}
		return pf.Accessibility().LocatorForWindow(managedWindowID, selector, o.snapshot()), nil
	}
	return pf.Accessibility().LocatorForApplication(accessibility.ApplicationFilter{
		Name: strings.TrimSpace(o.appName), PID: o.pid,
	}, selector, o.snapshot()), nil
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
	path := strings.TrimSpace(o.path)
	if !dbus.ObjectPath(path).IsValid() {
		return accessibility.NodeID{}, fmt.Errorf("raw node operation requires a valid D-Bus object path; got %q", path)
	}
	return accessibility.NodeID{BusName: strings.TrimSpace(o.bus), ObjectPath: path, Generation: o.generation}, nil
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
		return writeAccessibilityApplications(w, typed)
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
		return writeAccessibilityOutline(w, typed)
	case accessibility.Action:
		_, err := fmt.Fprintf(w, "action index=%d name=%q localized=%q description=%q key=%q\n", typed.Index, typed.Name, typed.LocalizedName, typed.Description, typed.KeyBinding)
		return err
	case accessibility.ActionReceipt:
		if err := writeAccessibilityNode(w, typed.Node, "target "); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "action index=%d name=%q localized=%q mechanism=%s generation=%d\n", typed.Action.Index, typed.Action.Name, typed.Action.LocalizedName, typed.Mechanism, typed.Generation)
		return err
	case accessibilityActionCLIResult:
		if _, err := fmt.Fprintf(w, "action status=%s dispatch=%s outcome=%s evaluations=%d wakeups=%d error=%s\n", typed.Status, typed.Receipt.Dispatch, typed.Receipt.Outcome.Status, typed.Evidence.Evaluations, typed.Evidence.Wakeups, typed.ErrorCode); err != nil {
			return err
		}
		return writeAccessibilityText(w, typed.Receipt)
	case accessibilityWaitCLIResult:
		return writeAccessibilityWaitResult(w, typed)
	default:
		_, err := fmt.Fprintln(w, value)
		return err
	}
}

func writeAccessibilityApplications(w io.Writer, apps []accessibility.Application) error {
	for _, app := range apps {
		if _, err := fmt.Fprintf(w, "%s\tpid=%d\t%s/%s@%d\n", app.Name, app.PID, app.ID.BusName, app.ID.ObjectPath, app.ID.Generation); err != nil {
			return err
		}
	}
	return nil
}

func writeAccessibilityWaitResult(w io.Writer, result accessibilityWaitCLIResult) error {
	if _, err := fmt.Fprintf(w, "wait state=%s satisfied=%t evaluations=%d wakeups=%d\n", result.State, result.Satisfied, result.Evidence.Evaluations, result.Evidence.Wakeups); err != nil {
		return err
	}
	if result.Node != nil {
		return writeAccessibilityNode(w, *result.Node, "target ")
	}
	return nil
}

type accessibilityWaitCLIResult struct {
	State     perfuncted.AccessibilityWaitState `json:"state"`
	Satisfied bool                              `json:"satisfied"`
	Node      *accessibility.Node               `json:"node,omitempty"`
	Evidence  perfuncted.WaitEvidence           `json:"evidence"`
}

type accessibilityActionCLIResult struct {
	Status    string                      `json:"status"`
	Receipt   accessibility.ActionReceipt `json:"receipt"`
	Evidence  perfuncted.WaitEvidence     `json:"evidence"`
	ErrorCode string                      `json:"errorCode,omitempty"`
}

// writeAccessibilityOutline preserves incomplete-tree diagnostic evidence.
func writeAccessibilityOutline(w io.Writer, outline accessibility.Outline) error {
	if err := writeAccessibilityOutlineNode(w, outline.Root, ""); err != nil {
		return err
	}
	if outline.Truncated || outline.ProviderErrors > 0 {
		_, err := fmt.Fprintf(w, "[partial generation=%d truncated=%t provider_errors=%d warnings=%v]\n", outline.Generation, outline.Truncated, outline.ProviderErrors, outline.Warnings)
		return err
	}
	return nil
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

func parseAccessibilityWaitState(value string) (perfuncted.AccessibilityWaitState, error) {
	state := perfuncted.AccessibilityWaitState(strings.ToLower(strings.TrimSpace(value)))
	switch state {
	case perfuncted.AccessibilityWaitPresent,
		perfuncted.AccessibilityWaitAbsent,
		perfuncted.AccessibilityWaitFocused,
		perfuncted.AccessibilityWaitUnfocused,
		perfuncted.AccessibilityWaitVisible,
		perfuncted.AccessibilityWaitHidden,
		perfuncted.AccessibilityWaitEnabled,
		perfuncted.AccessibilityWaitDisabled,
		perfuncted.AccessibilityWaitValueChanged:
		return state, nil
	default:
		return "", fmt.Errorf("accessibility: unknown --until state %q", value)
	}
}

func openAccessibilitySession(ctx context.Context, openPF sessionOpener) (*perfuncted.Session, error) {
	pf, err := openPF(ctx)
	if err != nil {
		return nil, err
	}
	if pf == nil || !pf.Has(perfuncted.CapabilityAccessibility) {
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
		value, err := pf.Accessibility().WithContext(c.Context()).Applications()
		if err != nil {
			return err
		}
		return accessibilityOutput(c.OutOrStdout(), appsJSON, value)
	}}
	addJSONFlag(apps, &appsJSON)

	var treeOpts accessibilityCLIOptions
	tree := &cobra.Command{Use: "tree", Short: "Capture a bounded tree for one application or managed window", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := treeOpts.requireScope(); err != nil {
			return err
		}
		pf, err := openAccessibilityScopeSession(c.Context(), openPF, openWindowPF, treeOpts)
		if err != nil {
			return err
		}
		defer pf.Close()
		root, err := treeOpts.root(c.Context(), pf)
		if err != nil {
			return err
		}
		snapshot, err := pf.Accessibility().WithContext(c.Context()).Snapshot(root, treeOpts.snapshot())
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
	find := &cobra.Command{
		Use:   "find",
		Short: "Find semantic nodes in one application or managed window",
		Long:  "Query a fresh, structurally complete AT-SPI snapshot. The command fails when provider behavior or snapshot bounds prevent a complete traversal.",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := findOpts.requireScope(); err != nil {
				return err
			}
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
			value, err := pf.Accessibility().WithContext(c.Context()).Find(root, findQuery, findOpts.snapshot())
			if err != nil {
				return err
			}
			return accessibilityOutput(c.OutOrStdout(), findOpts.json, value)
		},
	}
	addJSONFlag(find, &findOpts.json)
	addScopeFlags(find, &findOpts)
	addSnapshotFlags(find, &findOpts)
	addQueryFlags(find, &findQuery, &findAttributes)

	var waitOpts accessibilityCLIOptions
	var waitQuery accessibility.Query
	var waitAttributes []string
	var waitUntil string
	var waitTimeout, waitPoll time.Duration
	wait := &cobra.Command{
		Use:   "wait",
		Short: "Wait for a scoped accessible control state",
		Long:  "Wait for one uniquely matched accessible control using fresh complete snapshots. Notifications only wake the wait; they do not establish success. --state filters initial target selection, while --until names the state to wait for; after the first match, the wait follows that node identity. --timeout is required. --visible-only cannot be used to prove absence or a hidden state.",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := waitOpts.requireScope(); err != nil {
				return err
			}
			state, err := parseAccessibilityWaitState(waitUntil)
			if err != nil {
				return err
			}
			if waitTimeout <= 0 {
				return fmt.Errorf("accessibility: --timeout must be positive")
			}
			if waitPoll < 0 {
				return fmt.Errorf("accessibility: --poll must be nonnegative")
			}
			if waitOpts.visibleOnly && (state == perfuncted.AccessibilityWaitAbsent || state == perfuncted.AccessibilityWaitHidden) {
				return fmt.Errorf("accessibility: --visible-only cannot prove absence or a hidden state")
			}
			pf, err := openAccessibilityScopeSession(c.Context(), openPF, openWindowPF, waitOpts)
			if err != nil {
				return err
			}
			defer pf.Close()

			waitQuery.Attributes = parseAccessibilityAttributes(waitAttributes)
			locator, err := waitOpts.locator(c.Context(), pf, accessibility.Selector{
				Name: waitQuery.Name, Role: waitQuery.Role, Text: waitQuery.Text,
				States: waitQuery.States, Attributes: waitQuery.Attributes,
			})
			if err != nil {
				return err
			}
			var options []perfuncted.WaitOption
			if waitPoll > 0 {
				options = append(options, perfuncted.WaitEvery(waitPoll))
			}
			node, evidence, err := locator.WithContext(c.Context()).WaitForState(state, waitTimeout, options...)
			if err != nil {
				return err
			}
			result := accessibilityWaitCLIResult{State: state, Satisfied: true, Evidence: evidence}
			if state != perfuncted.AccessibilityWaitAbsent {
				result.Node = &node
			}
			return accessibilityOutput(c.OutOrStdout(), waitOpts.json, result)
		},
	}
	addJSONFlag(wait, &waitOpts.json)
	addScopeFlags(wait, &waitOpts)
	addSnapshotFlags(wait, &waitOpts)
	addQueryFlags(wait, &waitQuery, &waitAttributes)
	wait.Flags().StringVar(&waitUntil, "until", string(perfuncted.AccessibilityWaitPresent), "state to wait for: present, absent, focused, unfocused, visible, hidden, enabled, disabled, value-changed")
	wait.Flags().DurationVar(&waitTimeout, "timeout", 0, "required maximum wait duration (e.g. 5s)")
	wait.Flags().DurationVar(&waitPoll, "poll", 0, "fallback polling interval (defaults to the session interval)")

	var focusedJSON bool
	focused := &cobra.Command{Use: "focused", Short: "Show the currently focused accessible node", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		pf, err := openAccessibilitySession(c.Context(), openPF)
		if err != nil {
			return err
		}
		defer pf.Close()
		value, err := pf.Accessibility().WithContext(c.Context()).Focused(accessibility.SnapshotOptions{})
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
		// AT-SPI Component.GetAccessibleAtPoint takes signed 32-bit values.
		// Reject unrepresentable values before requesting desktop access.
		if int64(pointX) != int64(int32(pointX)) || int64(pointY) != int64(int32(pointY)) {
			return fmt.Errorf("at-point: screen coordinates exceed signed 32-bit range")
		}
		pf, err := openAccessibilitySession(c.Context(), openPF)
		if err != nil {
			return err
		}
		defer pf.Close()
		value, err := pf.Accessibility().WithContext(c.Context()).AtPoint(pointX, pointY)
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
		if eventBuffer < 0 {
			return fmt.Errorf("accessibility: --buffer must be nonnegative")
		}
		pf, err := openAccessibilitySession(c.Context(), openPF)
		if err != nil {
			return err
		}
		defer pf.Close()
		stream, err := pf.Accessibility().WithContext(c.Context()).Events(accessibility.EventOptions{Buffer: eventBuffer})
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
	var actionExpectText string
	var actionExpectState string
	var actionTimeout time.Duration
	action := &cobra.Command{
		Use:   "action",
		Short: "Invoke one semantic AT-SPI action and optionally verify its result",
		Long: `Invoke one uniquely resolved semantic AT-SPI action.

Optional --expect-text or --expect-state changed waits for fresh accessible
state with a bounded timeout. A verified result means the condition was
observed; it does not prove the action caused it. A timeout never retries the
action. Without an expectation, the existing one-shot receipt is returned.

Examples:
  pf a11y action --app Editor --role button --name Save --expect-text Saved --timeout 5s
  pf a11y action --app Editor --role button --name Save --expect-state changed --timeout 5s`,
		Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			if err := actionOpts.requireScope(); err != nil {
				return err
			}
			verificationRequested := c.Flags().Changed("expect-text") || c.Flags().Changed("expect-state")
			if c.Flags().Changed("timeout") && !verificationRequested {
				return fmt.Errorf("accessibility: --timeout requires --expect-text or --expect-state")
			}
			if verificationRequested && actionTimeout <= 0 {
				return fmt.Errorf("accessibility: --timeout must be positive")
			}
			if c.Flags().Changed("expect-text") && strings.TrimSpace(actionExpectText) == "" {
				return fmt.Errorf("accessibility: --expect-text must not be empty")
			}
			if c.Flags().Changed("expect-state") && actionExpectState != "changed" {
				return fmt.Errorf("accessibility: --expect-state supports only changed")
			}
			if c.Flags().Changed("expect-text") && c.Flags().Changed("expect-state") {
				return fmt.Errorf("accessibility: choose only one of --expect-text or --expect-state")
			}
			pf, err := openAccessibilityScopeSession(c.Context(), openPF, openWindowPF, actionOpts)
			if err != nil {
				return err
			}
			defer pf.Close()
			root, err := actionOpts.root(c.Context(), pf)
			if err != nil {
				if verificationRequested {
					result := accessibilityActionCLIResult{Status: "preflight-failed", ErrorCode: "preflight-failed"}
					if outputErr := accessibilityOutput(c.OutOrStdout(), actionOpts.json, result); outputErr != nil {
						return outputErr
					}
				}
				return err
			}
			actionQuery.Attributes = parseAccessibilityAttributes(actionAttributes)
			if !verificationRequested {
				receipt, err := pf.Accessibility().WithContext(c.Context()).InvokeSemanticAction(root, actionQuery, actionName, actionOpts.snapshot())
				if err != nil {
					return err
				}
				return accessibilityOutput(c.OutOrStdout(), actionOpts.json, receipt)
			}

			var postcondition perfuncted.Condition
			if c.Flags().Changed("expect-text") {
				postcondition = perfuncted.AccessibilityTextContains(root, actionExpectText, actionOpts.snapshot())
			} else {
				baseline, findErr := pf.Accessibility().WithContext(c.Context()).FindOne(root, actionQuery, actionOpts.snapshot())
				if findErr != nil {
					result := accessibilityActionCLIResult{Status: "preflight-failed", ErrorCode: "preflight-failed"}
					if outputErr := accessibilityOutput(c.OutOrStdout(), actionOpts.json, result); outputErr != nil {
						return outputErr
					}
					return findErr
				}
				postcondition = perfuncted.AccessibilityNodeChanged(root, baseline, actionOpts.snapshot())
			}

			verifyCtx, cancel := context.WithTimeout(c.Context(), actionTimeout)
			defer cancel()
			receipt, evidence, actionErr := pf.WithContext(verifyCtx).InvokeAccessibilityActionAndWait(
				root, actionQuery, actionName, actionOpts.snapshot(), postcondition,
			)
			result := accessibilityActionCLIResult{Status: "verified", Receipt: receipt, Evidence: evidence}
			if actionErr != nil {
				result.Status = "action-failed"
				result.ErrorCode = "action-failed"
				if receipt.Dispatch == "" {
					result.Status = "preflight-failed"
					result.ErrorCode = "preflight-failed"
				}
				var verificationErr *perfuncted.ActionVerificationError
				if errors.As(actionErr, &verificationErr) {
					result.Status = "verification-failed"
					result.ErrorCode = "verification-failed"
					if errors.Is(actionErr, context.DeadlineExceeded) {
						result.Status = "verification-timeout"
						result.ErrorCode = "deadline-exceeded"
					}
				}
			}
			if outputErr := accessibilityOutput(c.OutOrStdout(), actionOpts.json, result); outputErr != nil {
				return outputErr
			}
			return actionErr
		}}
	addJSONFlag(action, &actionOpts.json)
	addScopeFlags(action, &actionOpts)
	addSnapshotFlags(action, &actionOpts)
	addQueryFlags(action, &actionQuery, &actionAttributes)
	action.Flags().StringVar(&actionName, "action", "", "exact action name; empty selects the provider's first action")
	action.Flags().StringVar(&actionExpectText, "expect-text", "", "verify that accessible state exposes this text after the action")
	action.Flags().StringVar(&actionExpectState, "expect-state", "", "verify an observable state transition; supported value: changed")
	action.Flags().DurationVar(&actionTimeout, "timeout", 5*time.Second, "maximum time for a verified action (must be positive)")

	var focusOpts accessibilityCLIOptions
	var focusQuery accessibility.Query
	var focusAttributes []string
	focus := &cobra.Command{Use: "focus", Short: "Resolve one semantic node and request AT-SPI focus", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := focusOpts.requireScope(); err != nil {
			return err
		}
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
		node, err := pf.Accessibility().WithContext(c.Context()).FindOne(root, focusQuery, focusOpts.snapshot())
		if err != nil {
			return err
		}
		if err := pf.Accessibility().WithContext(c.Context()).FocusNode(node.ID); err != nil {
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
		if err := textOpts.requireScope(); err != nil {
			return err
		}
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
		node, err := pf.Accessibility().WithContext(c.Context()).FindOne(root, textQuery, textOpts.snapshot())
		if err != nil {
			return err
		}
		if err := pf.Accessibility().WithContext(c.Context()).ReplaceEditableText(node.ID, textValue); err != nil {
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
		// Invalid syntax must not open AT-SPI connections or prompt users for
		// desktop accessibility authorization. Reopen alone needs no handle.
		op := strings.ToLower(strings.TrimSpace(rawOp))
		if op == "" {
			return fmt.Errorf("raw requires --op")
		}
		var node accessibility.NodeID
		if op != "reopen" {
			var err error
			node, err = rawOpts.node()
			if err != nil {
				return err
			}
		}
		// Pure request validation precedes any D-Bus connection or portal
		// authorization, including typed values and operation names.
		inv := rawInvocation{op: op, actionName: rawActionName, actionIndex: rawActionIndex, alignment: rawAlignment, coord: rawCoord, x: rawX, y: rawY, width: rawWidth, height: rawHeight, value: rawValue, text: rawText, rangeText: rawRangeText, selectionsJSON: rawSelectionsJSON, start: rawStart, end: rawEnd, offset: rawOffset, selection: rawSelection, position: rawPosition, index: rawIndex}
		if op != "reopen" {
			if err := validateRawInvocation(inv); err != nil {
				return err
			}
		}
		pf, err := openAccessibilitySession(c.Context(), openPF)
		if err != nil {
			return err
		}
		defer pf.Close()
		if op == "reopen" {
			if reopenErr := pf.Accessibility().WithContext(c.Context()).Reopen(); reopenErr != nil {
				return reopenErr
			}
			return accessibilityOutput(c.OutOrStdout(), rawOpts.json, "ok")
		}
		value, err := invokeRawOp(c.Context(), pf.Accessibility(), node, inv)
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

	cmd.AddCommand(apps, tree, find, wait, focused, atPoint, events, action, focus, text, raw)
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

// validateRawInvocation rejects malformed local syntax before backend access.
// Backend-dependent authorization, node generations and bounds remain the
// responsibility of the typed accessibility operations themselves.
func validateRawInvocation(in rawInvocation) error {
	switch in.op {
	case "action", "focus", "set-size", "set-value", "set-text-contents", "insert-text", "delete-text", "copy-text", "cut-text", "paste-text", "set-caret", "set-text-selection", "add-text-selection", "remove-text-selection", "select-child", "deselect-child", "select-all", "clear-selection", "deselect-selected-child", "select-row", "deselect-row", "select-column", "deselect-column":
		return nil
	case "scroll":
		_, err := parseAccessibilityScrollType(in.alignment)
		return err
	case "scroll-to-point", "set-position", "set-extents":
		_, err := parseAccessibilityCoordType(in.coord)
		return err
	case "set-document-text-selections":
		var selections []accessibility.DocumentTextSelection
		if err := json.Unmarshal([]byte(in.selectionsJSON), &selections); err != nil {
			return fmt.Errorf("invalid --selections JSON: %w", err)
		}
		// Empty batches cannot be applied by the AT-SPI Document backend.
		// Reject them before opening a desktop authorization session.
		if len(selections) == 0 {
			return fmt.Errorf("accessibility: document selections list must not be empty")
		}
		return nil
	default:
		return fmt.Errorf("unknown raw op %q", in.op)
	}
}

func invokeRawOp(ctx context.Context, bundle perfuncted.AccessibilityBundle, node accessibility.NodeID, in rawInvocation) (any, error) { //nolint:gocyclo // one dispatch table over the typed primitive surface.
	switch in.op {
	case "action":
		if strings.TrimSpace(in.actionName) != "" {
			return bundle.WithContext(ctx).InvokeActionByName(node, in.actionName)
		}
		if in.actionIndex >= 0 {
			return nil, bundle.WithContext(ctx).InvokeAction(node, in.actionIndex)
		}
		return bundle.WithContext(ctx).InvokeDefaultAction(node)
	case "focus":
		return nil, bundle.WithContext(ctx).FocusNode(node)
	case "scroll":
		kind, err := parseAccessibilityScrollType(in.alignment)
		if err != nil {
			return nil, err
		}
		return nil, bundle.WithContext(ctx).ScrollTo(node, kind)
	case "scroll-to-point":
		coord, err := parseAccessibilityCoordType(in.coord)
		if err != nil {
			return nil, err
		}
		return nil, bundle.WithContext(ctx).ScrollToPoint(node, coord, in.x, in.y)
	case "set-position":
		coord, err := parseAccessibilityCoordType(in.coord)
		if err != nil {
			return nil, err
		}
		return nil, bundle.WithContext(ctx).SetPosition(node, in.x, in.y, coord)
	case "set-size":
		return nil, bundle.WithContext(ctx).SetSize(node, in.width, in.height)
	case "set-extents":
		coord, err := parseAccessibilityCoordType(in.coord)
		if err != nil {
			return nil, err
		}
		return nil, bundle.WithContext(ctx).SetExtents(node, in.x, in.y, in.width, in.height, coord)
	case "set-value":
		return nil, bundle.WithContext(ctx).SetValue(node, in.value)
	case "set-text-contents":
		return nil, bundle.WithContext(ctx).ReplaceEditableText(node, in.text)
	case "insert-text":
		text := in.rangeText
		if text == "" {
			text = in.text
		}
		return nil, bundle.WithContext(ctx).InsertText(node, in.offset, text)
	case "delete-text":
		return nil, bundle.WithContext(ctx).DeleteText(node, in.start, in.end)
	case "copy-text":
		return nil, bundle.WithContext(ctx).CopyText(node, in.start, in.end)
	case "cut-text":
		return nil, bundle.WithContext(ctx).CutText(node, in.start, in.end)
	case "paste-text":
		return nil, bundle.WithContext(ctx).PasteText(node, in.position)
	case "set-caret":
		return nil, bundle.WithContext(ctx).SetCaretOffset(node, in.offset)
	case "set-text-selection":
		return nil, bundle.WithContext(ctx).SetTextSelection(node, in.selection, in.start, in.end)
	case "add-text-selection":
		return nil, bundle.WithContext(ctx).AddTextSelection(node, in.start, in.end)
	case "remove-text-selection":
		return nil, bundle.WithContext(ctx).RemoveTextSelection(node, in.selection)
	case "set-document-text-selections":
		var selections []accessibility.DocumentTextSelection
		if err := json.Unmarshal([]byte(in.selectionsJSON), &selections); err != nil {
			return nil, fmt.Errorf("invalid --selections JSON: %w", err)
		}
		return nil, bundle.WithContext(ctx).SetTextSelections(node, selections)
	case "select-child":
		return nil, bundle.WithContext(ctx).SelectChild(node, in.index)
	case "deselect-child":
		return nil, bundle.WithContext(ctx).DeselectChild(node, in.index)
	case "select-all":
		return nil, bundle.WithContext(ctx).SelectAll(node)
	case "clear-selection":
		return nil, bundle.WithContext(ctx).ClearSelection(node)
	case "deselect-selected-child":
		return nil, bundle.WithContext(ctx).DeselectSelectedChild(node)
	case "select-row":
		return nil, bundle.WithContext(ctx).SelectRow(node, in.index)
	case "deselect-row":
		return nil, bundle.WithContext(ctx).DeselectRow(node, in.index)
	case "select-column":
		return nil, bundle.WithContext(ctx).SelectColumn(node, in.index)
	case "deselect-column":
		return nil, bundle.WithContext(ctx).DeselectColumn(node, in.index)
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
