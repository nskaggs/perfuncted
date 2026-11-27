package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/capture"
	"github.com/nskaggs/perfuncted/v2/internal/compositor"
	diagnostic "github.com/nskaggs/perfuncted/v2/internal/diagnostic"
	"github.com/nskaggs/perfuncted/v2/internal/probe"
	"github.com/nskaggs/perfuncted/v2/internal/util"
)

// FailureBundleOptions controls an opt-in diagnostic capture.
//
// A bundle can contain screenshots, window titles, process IDs, and other
// desktop state. Callers should treat the directory as sensitive and should
// not upload it without explicit user consent.
type FailureBundleOptions struct {
	// Directory is the directory to create or update. When empty, a temporary
	// directory is created and its path is returned by CaptureFailureBundle.
	Directory string
	// Operation identifies the operation or test that failed.
	Operation string
	// Error identifies whether a failure prompted the capture. Manifest format
	// version 2 records only its presence because provider errors can contain
	// user supplied text.
	Error error
	// Metadata contains caller-supplied context to include in manifest.json.
	Metadata map[string]string
	// Accessibility adds a bounded, content-redacted semantic evidence artifact.
	// Snapshot and event values are summarized; names, descriptions, text,
	// selector values, action conditions, and provider warnings are omitted.
	Accessibility *AccessibilityFailureEvidence
	// IncludeInputReceipts adds the bounded recent input operation history.
	// Receipts omit key and text payloads, coordinates, and raw error messages.
	IncludeInputReceipts bool
}

// AccessibilityFailureEvidence supplies the semantic context associated with
// a failure. Values are summarized into accessibility.json so input contents
// and provider text do not enter the artifact.
type AccessibilityFailureEvidence struct {
	Locator       *AccessibilityLocator
	Snapshot      *accessibility.Snapshot
	Events        []accessibility.Event
	Action        *accessibility.ActionReceipt
	Postcondition *WaitEvidence
}

// CaptureFailureBundle collects best-effort diagnostic artifacts into a
// directory and returns its path. Each artifact is attempted independently;
// the returned error reports collection failures after the successful files
// have been written. Its manifest records the effective timeout policy and
// whether each field came from SessionConfig or the library defaults.
func (s *sessionCore) CaptureFailureBundle(ctx context.Context, options FailureBundleOptions) (string, error) {
	if s == nil {
		return "", ErrNilSession
	}
	if ctx == nil {
		return "", fmt.Errorf("perfuncted: capture failure bundle: %w: nil context", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	directory := options.Directory
	if directory == "" {
		var err error
		directory, err = os.MkdirTemp("", "perfuncted-failure-")
		if err != nil {
			return "", fmt.Errorf("perfuncted: create failure bundle directory: %w", err)
		}
	} else if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("perfuncted: create failure bundle directory %q: %w", directory, err)
	}

	manifest := failureManifest{
		FormatVersion:     2,
		CapturedAt:        time.Now().UTC(),
		Operation:         options.Operation,
		Target:            string(s.Target().Kind()),
		Compositor:        compositor.DetectRuntime(s.env).String(), //nolint:contextcheck // Runtime detection has no contextual API; bundle records a best-effort snapshot.
		Environment:       diagnostic.Environment(s.env.EnvList()),
		Build:             diagnostic.CurrentBuildInfo(),
		LogDirectory:      s.LogPath(),
		Metadata:          copyStringMap(options.Metadata),
		Capabilities:      diagnosticCapabilities(s.Capabilities()),
		Timeouts:          s.Timeouts(),
		TimeoutProvenance: s.timeoutProvenance(),
		Probes:            diagnostic.Probes(s.env), //nolint:contextcheck // probe APIs are synchronous and do not accept a context.
		ArtifactErrors:    make(map[string]string),
	}
	if options.Error != nil {
		manifest.ErrorPresent = true
	}

	artifactErrors := s.captureFailureArtifacts(ctx, directory, &manifest, options)

	manifest.Artifacts = uniqueSortedStrings(manifest.Artifacts)
	manifest.ArtifactErrors = copyStringMap(manifest.ArtifactErrors)
	if err := diagnostic.WriteJSON(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		artifactErrors = errors.Join(artifactErrors, fmt.Errorf("manifest.json: %w", err))
	}
	return directory, artifactErrors
}

type failureArtifactCollector struct {
	manifest *failureManifest
	errors   []error
}

func (c *failureArtifactCollector) capture(name string, fn func() error) {
	if err := fn(); err != nil {
		// Provider errors can include private desktop content or filesystem
		// paths. Preserve the full error for the caller, not the disk manifest.
		c.manifest.ArtifactErrors[name] = "capture failed (details redacted)"
		c.errors = append(c.errors, fmt.Errorf("%s: %w", name, err))
		return
	}
	c.manifest.Artifacts = append(c.manifest.Artifacts, name)
}

func (s *sessionCore) captureFailureArtifacts(ctx context.Context, directory string, manifest *failureManifest, options FailureBundleOptions) error {
	collector := failureArtifactCollector{manifest: manifest}
	collector.capture("screenshot.png", func() error { return s.captureScreenshot(ctx, directory) })
	collector.capture("windows.json", func() error { return s.captureWindows(ctx, directory) })
	collector.capture("active-window.json", func() error { return s.captureActiveWindow(ctx, directory) })
	collector.capture("outputs.json", func() error { return s.captureOutputs(ctx, directory) })
	collector.capture("trace.txt", func() error { return s.captureTrace(directory) })
	if options.Accessibility != nil {
		collector.capture("accessibility.json", func() error {
			return diagnostic.WriteJSON(filepath.Join(directory, "accessibility.json"), summarizeAccessibilityFailure(options.Accessibility))
		})
	}
	if options.IncludeInputReceipts {
		collector.capture("input-receipts.json", func() error {
			receipts := s.input.RecentInputReceipts()
			if receipts == nil {
				receipts = []input.ActionReceipt{}
			}
			return diagnostic.WriteJSON(filepath.Join(directory, "input-receipts.json"), summarizeInputReceipts(receipts))
		})
	}
	return errors.Join(collector.errors...)
}

func (s *sessionCore) diagnosticContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, s.Timeouts().Diagnostic)
}

func (s *sessionCore) captureScreenshot(ctx context.Context, directory string) error {
	if s.screen == nil || util.IsNil(s.screen.backend()) {
		return errors.New("screen capability is not configured")
	}
	ctx, cancel := s.diagnosticContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	img, err := s.screen.backend().Grab(ctx, image.Rectangle{})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return err
	}
	if util.IsNil(img) {
		return errors.New("screen backend returned a nil image")
	}
	// Unlike the public ScreenBundle path, diagnostics read the capture
	// backend directly. Reject truncated planes before PNG accesses them.
	if err := capture.ValidateImageLayout(img); err != nil {
		return err
	}
	return diagnostic.WritePNG(filepath.Join(directory, "screenshot.png"), img)
}

func (s *sessionCore) captureWindows(ctx context.Context, directory string) error {
	if s.windows == nil || util.IsNil(s.windows.backend()) {
		return errors.New("window capability is not configured")
	}
	ctx, cancel := s.diagnosticContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	windows, err := s.windows.backend().List(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return err
	}
	return diagnostic.WriteJSON(filepath.Join(directory, "windows.json"), windows)
}

func (s *sessionCore) captureActiveWindow(ctx context.Context, directory string) error {
	if s.windows == nil || util.IsNil(s.windows.backend()) {
		return errors.New("window capability is not configured")
	}
	ctx, cancel := s.diagnosticContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	title, err := s.windows.backend().ActiveTitle(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return err
	}
	return diagnostic.WriteJSON(filepath.Join(directory, "active-window.json"), struct {
		Title string `json:"title"`
	}{Title: title})
}

func (s *sessionCore) captureOutputs(ctx context.Context, directory string) error {
	if s.outputs == nil || util.IsNil(s.outputs.Backend()) {
		return errors.New("output capability is not configured")
	}
	ctx, cancel := s.diagnosticContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	outputs, err := s.outputs.DiagnosticList(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return err
	}
	return diagnostic.WriteJSON(filepath.Join(directory, "outputs.json"), outputs)
}

func (s *sessionCore) captureTrace(directory string) error {
	trace := strings.Join(s.tracer.Recent(), "\n")
	if trace != "" {
		trace += "\n"
	}
	return os.WriteFile(filepath.Join(directory, "trace.txt"), []byte(trace), 0o600)
}

type failureManifest struct {
	FormatVersion     int                       `json:"format_version"`
	CapturedAt        time.Time                 `json:"captured_at"`
	Operation         string                    `json:"operation,omitempty"`
	ErrorPresent      bool                      `json:"error_present,omitempty"`
	Target            string                    `json:"target"`
	Compositor        string                    `json:"compositor"`
	Environment       map[string]string         `json:"environment"`
	Build             diagnostic.BuildInfo      `json:"build"`
	LogDirectory      string                    `json:"log_directory,omitempty"`
	Metadata          map[string]string         `json:"metadata,omitempty"`
	Capabilities      []diagnosticCapability    `json:"capabilities"`
	Timeouts          TimeoutPolicy             `json:"timeouts"`
	TimeoutProvenance map[string]string         `json:"timeout_provenance"`
	Probes            map[string][]probe.Result `json:"probes"`
	Artifacts         []string                  `json:"artifacts"`
	ArtifactErrors    map[string]string         `json:"artifact_errors,omitempty"`
}

func (s *sessionCore) timeoutProvenance() map[string]string {
	configured := s.config.Timeouts
	if s.hasTimeoutInput {
		configured = s.timeoutInput
	}
	return map[string]string{
		"short":      timeoutFieldProvenance(configured.Short),
		"medium":     timeoutFieldProvenance(configured.Medium),
		"long":       timeoutFieldProvenance(configured.Long),
		"startup":    timeoutFieldProvenance(configured.Startup),
		"diagnostic": timeoutFieldProvenance(configured.Diagnostic),
		"poll":       timeoutFieldProvenance(configured.Poll),
	}
}

func timeoutFieldProvenance(value time.Duration) string {
	if value > 0 {
		return "session_config"
	}
	return "library_default"
}

type diagnosticCapability struct {
	Capability  string   `json:"capability"`
	Requested   bool     `json:"requested"`
	Required    bool     `json:"required"`
	Available   bool     `json:"available"`
	Backend     string   `json:"backend,omitempty"`
	Operations  []string `json:"operations,omitempty"`
	Diagnostics []string `json:"diagnostics,omitempty"`
	Failure     string   `json:"failure,omitempty"`
}

func diagnosticCapabilities(statuses []CapabilityStatus) []diagnosticCapability {
	out := make([]diagnosticCapability, 0, len(statuses))
	for _, status := range statuses {
		item := diagnosticCapability{
			Capability:  string(status.Capability),
			Requested:   status.Requested,
			Required:    status.Required,
			Available:   status.Available,
			Backend:     status.Backend,
			Operations:  append([]string(nil), status.Operations...),
			Diagnostics: append([]string(nil), status.Diagnostics...),
		}
		if status.Failure != nil {
			// Backend errors may include window titles, filesystem paths, or
			// other provider-controlled text. Diagnostic manifests record only
			// failure presence, matching the top-level error privacy contract.
			item.Failure = "reported (details redacted)"
		}
		out = append(out, item)
	}
	return out
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	// The artifact set is small; insertion sort avoids another dependency.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

const (
	maxDiagnosticAccessibilityNodes  = 64
	maxDiagnosticAccessibilityEvents = 64
)

type accessibilityFailureArtifact struct {
	Locator       *locatorDiagnosticSummary  `json:"locator,omitempty"`
	Snapshot      *snapshotDiagnosticSummary `json:"snapshot,omitempty"`
	Events        []eventDiagnosticSummary   `json:"events,omitempty"`
	EventsOmitted int                        `json:"events_omitted,omitempty"`
	Action        *actionDiagnosticSummary   `json:"action,omitempty"`
	Postcondition *waitDiagnosticSummary     `json:"postcondition,omitempty"`
}

type locatorDiagnosticSummary struct {
	Scope          string   `json:"scope"`
	Role           string   `json:"role,omitempty"`
	HasName        bool     `json:"has_name_selector"`
	HasLabel       bool     `json:"has_label_selector"`
	HasText        bool     `json:"has_text_selector"`
	States         []string `json:"states,omitempty"`
	AttributeCount int      `json:"attribute_count,omitempty"`
	AncestorCount  int      `json:"ancestor_count,omitempty"`
	FreshRequested bool     `json:"fresh_requested"`
	MaxDepth       int      `json:"max_depth,omitempty"`
	MaxNodes       int      `json:"max_nodes,omitempty"`
	MaxTextBytes   int      `json:"max_text_bytes,omitempty"`
	MaxTotalBytes  int      `json:"max_total_bytes,omitempty"`
	VisibleOnly    bool     `json:"visible_only"`
	AllowSensitive bool     `json:"allow_sensitive"`
}

type snapshotDiagnosticSummary struct {
	Generation            uint64                  `json:"generation"`
	CapturedAt            string                  `json:"captured_at,omitempty"`
	Source                string                  `json:"source,omitempty"`
	Truncated             bool                    `json:"truncated"`
	TruncationReasonCount int                     `json:"truncation_reason_count,omitempty"`
	ProviderErrors        int                     `json:"provider_errors,omitempty"`
	WarningCount          int                     `json:"warning_count,omitempty"`
	NodeCount             int                     `json:"node_count"`
	OmittedNodes          int                     `json:"omitted_nodes,omitempty"`
	Nodes                 []nodeDiagnosticSummary `json:"nodes"`
}

type nodeDiagnosticSummary struct {
	ID             accessibility.NodeID `json:"id"`
	Parent         accessibility.NodeID `json:"parent,omitempty"`
	Role           string               `json:"role,omitempty"`
	Interfaces     []string             `json:"interfaces,omitempty"`
	States         []string             `json:"states,omitempty"`
	HasName        bool                 `json:"has_name"`
	HasDescription bool                 `json:"has_description"`
	HasText        bool                 `json:"has_text"`
	TextTruncated  bool                 `json:"text_truncated"`
	HasBounds      bool                 `json:"has_bounds"`
	Visible        bool                 `json:"visible"`
	Showing        bool                 `json:"showing"`
	Enabled        bool                 `json:"enabled"`
	Focused        bool                 `json:"focused"`
	Redacted       bool                 `json:"redacted"`
	ChildCount     int                  `json:"child_count"`
	WarningCount   int                  `json:"warning_count,omitempty"`
}

type eventDiagnosticSummary struct {
	Kind      string               `json:"kind,omitempty"`
	Node      accessibility.NodeID `json:"node"`
	Property  string               `json:"property,omitempty"`
	HasValue  bool                 `json:"has_value"`
	Dropped   uint64               `json:"dropped,omitempty"`
	Timestamp string               `json:"timestamp,omitempty"`
}

type actionDiagnosticSummary struct {
	Node         nodeDiagnosticSummary `json:"node"`
	Operation    string                `json:"operation,omitempty"`
	Mechanism    string                `json:"mechanism,omitempty"`
	ActionIndex  int32                 `json:"action_index"`
	Dispatch     string                `json:"dispatch"`
	DispatchedAt string                `json:"dispatched_at,omitempty"`
	Outcome      string                `json:"outcome"`
	ObservedAt   string                `json:"observed_at,omitempty"`
	HasCondition bool                  `json:"has_condition"`
}

type waitDiagnosticSummary struct {
	Evaluations          int                     `json:"evaluations"`
	Wakeups              int                     `json:"wakeups"`
	PollWakeups          int                     `json:"poll_wakeups"`
	WindowWakeups        int                     `json:"window_wakeups"`
	AccessibilityWakeups int                     `json:"accessibility_wakeups"`
	ApplicationWakeups   int                     `json:"application_wakeups"`
	LastWakeSource       string                  `json:"last_wake_source,omitempty"`
	HasCondition         bool                    `json:"has_condition"`
	LastEvent            *eventDiagnosticSummary `json:"last_event,omitempty"`
}

func summarizeAccessibilityFailure(evidence *AccessibilityFailureEvidence) accessibilityFailureArtifact {
	if evidence == nil {
		return accessibilityFailureArtifact{}
	}
	artifact := accessibilityFailureArtifact{}
	if evidence.Locator != nil {
		locator := evidence.Locator
		scope := "unknown"
		switch locator.scope.kind {
		case locatorWindowScope:
			scope = "window"
		case locatorApplicationScope:
			scope = "application"
		}
		selector := locator.selector
		artifact.Locator = &locatorDiagnosticSummary{
			Scope:          scope,
			Role:           diagnostic.Label(selector.Role),
			HasName:        selector.Name != "",
			HasLabel:       selector.Label != "",
			HasText:        selector.Text != "",
			States:         diagnostic.Labels(selector.States),
			AttributeCount: len(selector.Attributes),
			AncestorCount:  len(selector.Ancestors),
			FreshRequested: locator.options.Fresh,
			MaxDepth:       locator.options.MaxDepth,
			MaxNodes:       locator.options.MaxNodes,
			MaxTextBytes:   locator.options.MaxTextBytes,
			MaxTotalBytes:  locator.options.MaxTotalBytes,
			VisibleOnly:    locator.options.VisibleOnly,
			AllowSensitive: locator.options.AllowSensitive,
		}
	}
	if evidence.Snapshot != nil {
		snapshot := evidence.Snapshot
		count := len(snapshot.Nodes)
		limit := min(count, maxDiagnosticAccessibilityNodes)
		nodes := make([]nodeDiagnosticSummary, 0, limit)
		for _, node := range snapshot.Nodes[:limit] {
			nodes = append(nodes, summarizeAccessibilityNode(node))
		}
		artifact.Snapshot = &snapshotDiagnosticSummary{
			Generation:            snapshot.Generation,
			CapturedAt:            snapshot.CapturedAt.UTC().Format(time.RFC3339Nano),
			Source:                diagnostic.Label(snapshot.Source),
			Truncated:             snapshot.Truncated,
			TruncationReasonCount: len(snapshot.TruncationReasons),
			ProviderErrors:        snapshot.ProviderErrors,
			WarningCount:          len(snapshot.Warnings),
			NodeCount:             count,
			OmittedNodes:          count - limit,
			Nodes:                 nodes,
		}
	}
	eventCount := len(evidence.Events)
	for _, event := range evidence.Events[:min(eventCount, maxDiagnosticAccessibilityEvents)] {
		artifact.Events = append(artifact.Events, summarizeAccessibilityEvent(event))
	}
	artifact.EventsOmitted = eventCount - len(artifact.Events)
	if evidence.Action != nil {
		action := evidence.Action
		artifact.Action = &actionDiagnosticSummary{
			Node:         summarizeAccessibilityNode(action.Node),
			Operation:    diagnostic.Label(action.Operation),
			Mechanism:    diagnostic.Label(action.Mechanism),
			ActionIndex:  action.Action.Index,
			Dispatch:     diagnosticDispatch(action.Dispatch),
			DispatchedAt: diagnostic.Timestamp(action.DispatchedAt),
			Outcome:      diagnosticOutcome(action.Outcome.Status),
			ObservedAt:   diagnostic.Timestamp(action.Outcome.ObservedAt),
			HasCondition: action.Outcome.Condition != "",
		}
	}
	if evidence.Postcondition != nil {
		wait := evidence.Postcondition
		artifact.Postcondition = &waitDiagnosticSummary{
			Evaluations:          wait.Evaluations,
			Wakeups:              wait.Wakeups,
			PollWakeups:          wait.PollWakeups,
			WindowWakeups:        wait.WindowWakeups,
			AccessibilityWakeups: wait.AccessibilityWakeups,
			ApplicationWakeups:   wait.ApplicationWakeups,
			LastWakeSource:       diagnostic.Label(wait.LastWakeSource),
			HasCondition:         true,
		}
		if wait.LastAccessibilityEvent != nil {
			event := summarizeAccessibilityEvent(*wait.LastAccessibilityEvent)
			artifact.Postcondition.LastEvent = &event
		}
	}
	return artifact
}

func summarizeAccessibilityNode(node accessibility.Node) nodeDiagnosticSummary {
	return nodeDiagnosticSummary{
		ID:             diagnosticNodeID(node.ID),
		Parent:         diagnosticNodeID(node.Parent),
		Role:           diagnostic.Label(node.Role),
		Interfaces:     diagnostic.Labels(node.Interfaces),
		States:         diagnostic.Labels(node.States),
		HasName:        node.Name != "",
		HasDescription: node.Description != "",
		HasText:        node.Text != "",
		TextTruncated:  node.TextTruncated,
		HasBounds:      node.HasBounds,
		Visible:        node.Visible,
		Showing:        node.Showing,
		Enabled:        node.Enabled,
		Focused:        node.Focused,
		Redacted:       node.Redacted,
		ChildCount:     node.ChildCount,
		WarningCount:   len(node.Warnings),
	}
}

func summarizeAccessibilityEvent(event accessibility.Event) eventDiagnosticSummary {
	return eventDiagnosticSummary{
		Kind:      diagnostic.Label(event.Kind),
		Node:      diagnosticNodeID(event.Node),
		Property:  diagnostic.Label(event.Property),
		HasValue:  event.Value != "",
		Dropped:   event.Dropped,
		Timestamp: diagnostic.Timestamp(event.Timestamp),
	}
}

func diagnosticNodeID(id accessibility.NodeID) accessibility.NodeID {
	return accessibility.NodeID{
		BusName:     diagnostic.Label(id.BusName),
		ObjectPath:  diagnostic.Label(id.ObjectPath),
		Generation:  id.Generation,
		Incarnation: id.Incarnation,
	}
}

func summarizeInputReceipts(receipts []input.ActionReceipt) []input.ActionReceipt {
	limit := min(len(receipts), inputReceiptHistoryLimit)
	if limit == 0 {
		return []input.ActionReceipt{}
	}
	summary := make([]input.ActionReceipt, 0, limit)
	for _, receipt := range receipts[len(receipts)-limit:] {
		receipt.Operation = diagnostic.Label(receipt.Operation)
		receipt.Mechanism = diagnostic.Label(receipt.Mechanism)
		receipt.FailureClass = diagnostic.Label(receipt.FailureClass)
		summary = append(summary, receipt)
	}
	return summary
}

func diagnosticDispatch(value accessibility.DispatchOutcome) string {
	switch value {
	case accessibility.DispatchAccepted, accessibility.DispatchNotSent, accessibility.DispatchUnknown, accessibility.DispatchRejected:
		return string(value)
	default:
		return "unknown"
	}
}

func diagnosticOutcome(value accessibility.ActionOutcomeStatus) string {
	switch value {
	case accessibility.ActionOutcomeNotObserved, accessibility.ActionOutcomeVerified, accessibility.ActionOutcomeNotVerified:
		return string(value)
	default:
		return string(accessibility.ActionOutcomeNotObserved)
	}
}
