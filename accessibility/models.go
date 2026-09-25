package accessibility

import (
	"context"
	"sort"
	"strings"
	"time"
)

// NodeID is an AT-SPI object reference scoped to one backend generation.
// BusName and ObjectPath are opaque and should be retained together; object
// paths are not globally unique and Generation rejects stale references.
type NodeID struct {
	BusName    string `json:"busName"`
	ObjectPath string `json:"objectPath"`
	Generation uint64 `json:"generation"`
}

func (id NodeID) valid() bool {
	return id.BusName != "" && id.ObjectPath != "" && id.ObjectPath != string(nullObjectPath) && id.Generation > 0
}

// Rect is a screen-coordinate component rectangle in pixels.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Node is a bounded snapshot of one accessible object.
type Node struct {
	ID            NodeID            `json:"id"`
	Parent        NodeID            `json:"parent,omitempty"`
	Name          string            `json:"name,omitempty"`
	Description   string            `json:"description,omitempty"`
	Role          string            `json:"role,omitempty"`
	RoleID        uint32            `json:"roleId,omitempty"`
	Interfaces    []string          `json:"interfaces,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	States        []string          `json:"states,omitempty"`
	Text          string            `json:"text,omitempty"`
	TextTruncated bool              `json:"textTruncated,omitempty"`
	Bounds        Rect              `json:"bounds"`
	HasBounds     bool              `json:"hasBounds"`
	ChildCount    int               `json:"childCount"`
	Children      []NodeID          `json:"children,omitempty"`
	// Relations preserves the AT-SPI relation targets when an object exposes
	// them. The relation key is the normalized AT-SPI relation name.
	Relations map[string][]NodeID `json:"relations,omitempty"`
	// Value is populated lazily for objects advertising org.a11y.atspi.Value.
	Value     *ValueInfo     `json:"value,omitempty"`
	Actions   []Action       `json:"actions,omitempty"`
	Selection *SelectionInfo `json:"selection,omitempty"`
	Table     *TableInfo     `json:"table,omitempty"`
	Document  *DocumentInfo  `json:"document,omitempty"`
	Focused   bool           `json:"focused"`
	Visible   bool           `json:"visible"`
	Showing   bool           `json:"showing"`
	Enabled   bool           `json:"enabled"`
	// Redacted is true when sensitive/protected content was intentionally
	// removed. Callers can still use the node's role, bounds, and state.
	Redacted bool     `json:"redacted,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ValueInfo contains the optional AT-SPI value interface fields.
type ValueInfo struct {
	Current          float64 `json:"current"`
	Minimum          float64 `json:"minimum"`
	Maximum          float64 `json:"maximum"`
	MinimumIncrement float64 `json:"minimumIncrement"`
}

// Action describes one optional AT-SPI action exposed by an object. Name is
// the machine-readable value from Action.GetName; LocalizedName, Description,
// and KeyBinding come from the localized Action.GetActions metadata.
type Action struct {
	Index         int32  `json:"index"`
	Name          string `json:"name,omitempty"`
	LocalizedName string `json:"localizedName,omitempty"`
	Description   string `json:"description,omitempty"`
	KeyBinding    string `json:"keyBinding,omitempty"`
}

// SelectionInfo summarizes the optional selection interface.
type SelectionInfo struct {
	SelectedChildCount int32 `json:"selectedChildCount"`
}

// TableInfo summarizes the optional table interface.
type TableInfo struct {
	Rows    int32 `json:"rows"`
	Columns int32 `json:"columns"`
}

// DocumentInfo summarizes the optional document interface.
type DocumentInfo struct {
	Locale            string `json:"locale,omitempty"`
	CurrentPageNumber int32  `json:"currentPageNumber,omitempty"`
	PageCount         int32  `json:"pageCount,omitempty"`
}

// DocumentTextSelection describes a cross-object text selection exposed by
// AT-SPI Document (since 2.52). Offsets are character offsets in the
// corresponding Text objects, not UTF-8 byte offsets.
type DocumentTextSelection struct {
	StartObject   NodeID `json:"startObject"`
	StartOffset   int32  `json:"startOffset"`
	EndObject     NodeID `json:"endObject"`
	EndOffset     int32  `json:"endOffset"`
	StartIsActive bool   `json:"startIsActive"`
}

// Application describes an application root discovered on the accessibility
// desktop. Its root Node is also present in a subsequent Snapshot.
type Application struct {
	Node
	PID            int32  `json:"pid,omitempty"`
	ToolkitName    string `json:"toolkitName,omitempty"`
	ToolkitVersion string `json:"toolkitVersion,omitempty"`
}

// SnapshotOptions bounds all work and response size. Zero values select safe
// defaults; values above the hard limits are capped.
type SnapshotOptions struct {
	MaxDepth     int `json:"maxDepth,omitempty"`
	MaxNodes     int `json:"maxNodes,omitempty"`
	MaxTextBytes int `json:"maxTextBytes,omitempty"`
	// MaxTotalBytes bounds the serialized snapshot response, including all
	// node fields and the snapshot envelope. MaxTextBytes is an individual
	// UTF-8 byte cap; AT-SPI character offsets remain character-based.
	MaxTotalBytes int  `json:"maxTotalBytes,omitempty"`
	VisibleOnly   bool `json:"visibleOnly,omitempty"`
	// AllowSensitive disables the default redaction of AT-SPI sensitive and
	// protected text/value attributes. Use only for an explicit trusted flow.
	AllowSensitive bool `json:"allowSensitive,omitempty"`
	// AllowDesktopRoot explicitly opts into a bounded whole-desktop tree. It
	// is false by default so Snapshot and Find cannot accidentally traverse
	// every application when a caller omitted scope.
	AllowDesktopRoot bool `json:"allowDesktopRoot,omitempty"`
	// SkipRoles prevents traversal below nodes with one of these exact
	// normalized AT-SPI roles while retaining the node itself in the bounded
	// snapshot. It is intended for a narrowly scoped semantic search that has
	// an authoritative structural subtree to ignore, such as a console output
	// landmark that precedes its command input.
	SkipRoles []string `json:"skipRoles,omitempty"`
}

func (o SnapshotOptions) normalized() SnapshotOptions {
	if o.MaxDepth <= 0 {
		o.MaxDepth = defaultMaxDepth
	}
	if o.MaxNodes <= 0 {
		o.MaxNodes = defaultMaxNodes
	}
	if o.MaxTextBytes <= 0 {
		o.MaxTextBytes = defaultMaxText
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = defaultMaxTotal
	}
	if o.MaxDepth > absMaxDepth {
		o.MaxDepth = absMaxDepth
	}
	if o.MaxNodes > absMaxNodes {
		o.MaxNodes = absMaxNodes
	}
	if o.MaxTextBytes > absMaxText {
		o.MaxTextBytes = absMaxText
	}
	if o.MaxTotalBytes > absMaxTotal {
		o.MaxTotalBytes = absMaxTotal
	}
	if len(o.SkipRoles) > 0 {
		roles := make([]string, 0, len(o.SkipRoles))
		seen := make(map[string]struct{}, len(o.SkipRoles))
		for _, role := range o.SkipRoles {
			role = strings.ToLower(strings.TrimSpace(role))
			if role == "" {
				continue
			}
			if _, ok := seen[role]; ok {
				continue
			}
			seen[role] = struct{}{}
			roles = append(roles, role)
		}
		sort.Strings(roles)
		o.SkipRoles = roles
	}
	return o
}

// Query filters a bounded snapshot. Matching is case-insensitive substring
// matching; an empty field is ignored.
type Query struct {
	Name       string            `json:"name,omitempty"`
	Role       string            `json:"role,omitempty"`
	Text       string            `json:"text,omitempty"`
	States     []string          `json:"states,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// ApplicationFilter selects an application root. Empty fields are ignored;
// PID is an exact process match. WindowID and WindowTitle correlate through
// the managed window resolver when set.
type ApplicationFilter struct {
	Name        string `json:"name,omitempty"`
	PID         int32  `json:"pid,omitempty"`
	Bus         string `json:"busName,omitempty"`
	WindowID    string `json:"windowId,omitempty"`
	WindowTitle string `json:"windowTitle,omitempty"`
}

// Event is an invalidation-oriented AT-SPI signal. Node is stamped with the
// generation created by this signal and is valid when the event is enqueued.
// A later physical signal may advance the backend generation before a caller
// reads the event, so consumers must validate the handle and refresh a
// Snapshot before acting on it; event handles are not durable capabilities.
type Event struct {
	Kind      string    `json:"kind"`
	Node      NodeID    `json:"node"`
	Property  string    `json:"property,omitempty"`
	Value     string    `json:"value,omitempty"`
	Dropped   uint64    `json:"dropped,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// EventOptions bounds the notification stream.
type EventOptions struct {
	Buffer int `json:"buffer,omitempty"`
}

func (o EventOptions) normalized() EventOptions {
	if o.Buffer <= 0 {
		o.Buffer = defaultEventBuffer
	}
	if o.Buffer > 4096 {
		o.Buffer = 4096
	}
	return o
}

// Snapshot is a flat, parent-linked accessibility tree. Warnings identify
// objects that disappeared while traversing; Truncated indicates a limit.
type Snapshot struct {
	Root              Node      `json:"root"`
	Nodes             []Node    `json:"nodes"`
	Truncated         bool      `json:"truncated"`
	TruncationReasons []string  `json:"truncationReasons,omitempty"`
	ProviderErrors    int       `json:"providerErrors,omitempty"`
	Warnings          []string  `json:"warnings,omitempty"`
	Generation        uint64    `json:"generation"`
	CapturedAt        time.Time `json:"capturedAt"`
	Source            string    `json:"source,omitempty"`
}

// Backend is the core AT-SPI surface used by perfuncted's bundle. Typed
// mutation and window-resolution extensions are optional so deterministic
// fakes can implement only the operations under test.
type Backend interface {
	SupportedOperations() []string
	Applications(context.Context) ([]Application, error)
	Snapshot(context.Context, NodeID, SnapshotOptions) (Snapshot, error)
	Find(context.Context, NodeID, Query, SnapshotOptions) ([]Node, error)
	Focused(context.Context, SnapshotOptions) (Node, error)
	AtPoint(context.Context, int, int) (Node, error)
	Close() error
}

// ApplicationFinder is an optional extension implemented by runtime
// backends. It is separate from Backend so deterministic fakes can implement
// only the operations they need.
type ApplicationFinder interface {
	FindApplication(context.Context, ApplicationFilter) (Application, error)
}

// EventSource is an optional extension for backends that can receive AT-SPI
// signals. The stream is bounded and lossy by design.
type EventSource interface {
	Events(context.Context, EventOptions) (<-chan Event, error)
}

// GenerationSource exposes the monotonic cache/invalidation generation.
type GenerationSource interface {
	Generation() uint64
	Invalidate(NodeID)
}

// ActionInvoker exposes typed Action interface operations.
type ActionInvoker interface {
	InvokeAction(context.Context, NodeID, int32) error
	InvokeActionByName(context.Context, NodeID, string) (Action, error)
	InvokeDefaultAction(context.Context, NodeID) (Action, error)
}

// ComponentController exposes typed Component interface operations.
type ComponentController interface {
	GrabFocus(context.Context, NodeID) error
	ScrollTo(context.Context, NodeID, ScrollType) error
	ScrollToPoint(context.Context, NodeID, CoordType, int, int) error
	SetPosition(context.Context, NodeID, int, int, CoordType) error
	SetSize(context.Context, NodeID, int, int) error
	SetExtents(context.Context, NodeID, int, int, int, int, CoordType) error
}

// ValueController exposes the typed Value interface operation.
type ValueController interface {
	SetValue(context.Context, NodeID, float64) error
}

// TextController exposes typed Text and EditableText operations. Providers
// may return ErrUnsupported for operations not implemented by an object.
type TextController interface {
	SetTextContents(context.Context, NodeID, string) error
	InsertText(context.Context, NodeID, int32, string) error
	DeleteText(context.Context, NodeID, int32, int32) error
	CopyText(context.Context, NodeID, int32, int32) error
	CutText(context.Context, NodeID, int32, int32) error
	PasteText(context.Context, NodeID, int32) error
	SetCaretOffset(context.Context, NodeID, int32) error
	SetTextSelection(context.Context, NodeID, int32, int32, int32) error
	AddTextSelection(context.Context, NodeID, int32, int32) error
	RemoveTextSelection(context.Context, NodeID, int32) error
}

// DocumentController exposes cross-object document selections. Providers
// predating AT-SPI 2.52 may return ErrUnsupported.
type DocumentController interface {
	SetTextSelections(context.Context, NodeID, []DocumentTextSelection) error
}

// SelectionController exposes typed Selection interface operations.
type SelectionController interface {
	SelectChild(context.Context, NodeID, int32) error
	DeselectChild(context.Context, NodeID, int32) error
	SelectAll(context.Context, NodeID) error
	ClearSelection(context.Context, NodeID) error
	DeselectSelectedChild(context.Context, NodeID) error
}

// TableController exposes the row/column selection operations defined by the
// AT-SPI Table interface.
type TableController interface {
	SelectRow(context.Context, NodeID, int32) error
	DeselectRow(context.Context, NodeID, int32) error
	SelectColumn(context.Context, NodeID, int32) error
	DeselectColumn(context.Context, NodeID, int32) error
}

// Automation is the aggregate typed mutation surface implemented by the
// native backend.
type Automation interface {
	ActionInvoker
	ComponentController
	ValueController
	TextController
	DocumentController
	SelectionController
	TableController
}

// ScrollType identifies the AT-SPI scroll coordinate semantics.
type ScrollType uint32

const (
	ScrollTopLeft ScrollType = iota
	ScrollBottomRight
	ScrollTopEdge
	ScrollBottomEdge
	ScrollLeftEdge
	ScrollRightEdge
	ScrollAnyWhere
)

// CoordType identifies the coordinate space used by AT-SPI Component
// ScrollToPoint, SetPosition, and SetExtents. It is intentionally distinct
// from ScrollType, which describes scroll alignment for ScrollTo.
type CoordType uint32

const (
	CoordTypeScreen CoordType = iota
	CoordTypeWindow
	CoordTypeParent
)

// WindowTarget describes an authoritative Perfuncted window used to resolve
// an AT-SPI top-level window/dialog/frame.
type WindowTarget struct {
	ID    string
	Title string
	// PID is the compositor-reported process owner. It is authoritative when
	// present and is included in correlation evidence.
	PID int32
	// PIDHint is an optional narrowing signal used only when the compositor did
	// not report a PID. It must not be presented as compositor ownership
	// evidence.
	PIDHint int32
	AppID   string
	Bounds  Rect
	Active  bool
	Focused bool
}

// WindowScope is the correlated accessibility subtree root and evidence used
// to choose it. A root is never silently substituted with an application root.
type WindowScope struct {
	WindowID        string      `json:"windowId"`
	Title           string      `json:"title,omitempty"`
	Root            NodeID      `json:"root"`
	ApplicationRoot NodeID      `json:"applicationRoot"`
	Application     Application `json:"application"`
	PID             int32       `json:"pid,omitempty"`
	AppID           string      `json:"appId,omitempty"`
	Generation      uint64      `json:"generation"`
	Candidates      []Candidate `json:"candidates,omitempty"`
	Evidence        []string    `json:"evidence,omitempty"`
}

// WindowResolver resolves an authoritative compositor window to its
// corresponding AT-SPI top-level accessible subtree.
type WindowResolver interface {
	ResolveWindow(context.Context, WindowTarget) (WindowScope, error)
}

// Reopener creates a fresh backend for the same target session. It is
// intentionally explicit; no operation retries or reconnects in the
// background.
type Reopener interface {
	Reopen(context.Context) (Backend, error)
}

// OpenRuntime opens the accessibility bus associated with rt. The normal
// session bus only provides org.a11y.Bus.GetAddress; all chatty AT-SPI calls
// are made over the returned private accessibility bus.
