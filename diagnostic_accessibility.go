package perfuncted

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nskaggs/perfuncted/accessibility"
)

const (
	maxDiagnosticAccessibilityNodes  = 64
	maxDiagnosticAccessibilityEvents = 64
	maxDiagnosticLabelBytes          = 128
	maxDiagnosticListItems           = 16
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
			Role:           diagnosticLabel(selector.Role),
			HasName:        selector.Name != "",
			HasLabel:       selector.Label != "",
			HasText:        selector.Text != "",
			States:         diagnosticLabels(selector.States),
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
			Source:                diagnosticLabel(snapshot.Source),
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
			Operation:    diagnosticLabel(action.Operation),
			Mechanism:    diagnosticLabel(action.Mechanism),
			ActionIndex:  action.Action.Index,
			Dispatch:     diagnosticDispatch(action.Dispatch),
			DispatchedAt: diagnosticTimestamp(action.DispatchedAt),
			Outcome:      diagnosticOutcome(action.Outcome.Status),
			ObservedAt:   diagnosticTimestamp(action.Outcome.ObservedAt),
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
			LastWakeSource:       diagnosticLabel(wait.LastWakeSource),
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
		Role:           diagnosticLabel(node.Role),
		Interfaces:     diagnosticLabels(node.Interfaces),
		States:         diagnosticLabels(node.States),
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
		Kind:      diagnosticLabel(event.Kind),
		Node:      diagnosticNodeID(event.Node),
		Property:  diagnosticLabel(event.Property),
		HasValue:  event.Value != "",
		Dropped:   event.Dropped,
		Timestamp: diagnosticTimestamp(event.Timestamp),
	}
}

func diagnosticNodeID(id accessibility.NodeID) accessibility.NodeID {
	return accessibility.NodeID{
		BusName:    diagnosticLabel(id.BusName),
		ObjectPath: diagnosticLabel(id.ObjectPath),
		Generation: id.Generation,
	}
}

func diagnosticLabels(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	limit := min(len(values), maxDiagnosticListItems)
	labels := make([]string, 0, limit)
	for _, value := range values[:limit] {
		labels = append(labels, diagnosticLabel(value))
	}
	return labels
}

func diagnosticLabel(value string) string {
	var label strings.Builder
	label.Grow(min(len(value), maxDiagnosticLabelBytes))
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		if size == 0 || label.Len()+utf8.RuneLen(r) > maxDiagnosticLabelBytes {
			break
		}
		label.WriteRune(r)
		value = value[size:]
	}
	return label.String()
}

func diagnosticTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func summarizeInputReceipts(receipts []InputActionReceipt) []InputActionReceipt {
	limit := min(len(receipts), inputReceiptHistoryLimit)
	if limit == 0 {
		return []InputActionReceipt{}
	}
	summary := make([]InputActionReceipt, 0, limit)
	for _, receipt := range receipts[len(receipts)-limit:] {
		receipt.Operation = diagnosticLabel(receipt.Operation)
		receipt.Mechanism = diagnosticLabel(receipt.Mechanism)
		receipt.FailureClass = diagnosticLabel(receipt.FailureClass)
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

func diagnosticOutcome(value ActionOutcomeStatus) string {
	switch value {
	case ActionOutcomeNotObserved, ActionOutcomeVerified, ActionOutcomeNotVerified:
		return string(value)
	default:
		return string(ActionOutcomeNotObserved)
	}
}
