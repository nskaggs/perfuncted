// Package accessibility exposes the AT-SPI accessibility tree and typed
// automation operations when
// an accessibility bus is available. It deliberately uses the same godbus
// dependency as the rest of perfuncted so callers do not need CGO or libatspi.
package accessibility

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
)

var (
	// ErrUnsupported indicates that an optional AT-SPI operation is not
	// implemented by this backend or object.
	ErrUnsupported = errors.New("accessibility: operation unsupported")
	// ErrNotFound indicates that a query found no matching accessible object.
	ErrNotFound = errors.New("accessibility: object not found")
	// ErrAmbiguous indicates that a selector matched more than one application,
	// window, action, or other target.
	ErrAmbiguous = errors.New("accessibility: ambiguous application")
	// ErrStaleNode indicates that a node handle belongs to an older
	// accessibility generation and must not be reused.
	ErrStaleNode = errors.New("accessibility: stale node")
	// ErrDisconnected indicates that the target accessibility bus disconnected.
	ErrDisconnected = errors.New("accessibility: disconnected")
	// ErrScope indicates that a tree operation was requested without an
	// explicit application/window/root scope.
	ErrScope = errors.New("accessibility: explicit scope required")
	// ErrInvalidAction indicates that an action index is not exposed by a node.
	ErrInvalidAction = errors.New("accessibility: invalid action index")
	// ErrMutationRejected indicates that a provider declined a valid mutation.
	ErrMutationRejected = errors.New("accessibility: mutation rejected")
)

const (
	busService          = "org.a11y.Bus"
	busAddressMethod    = busService + ".GetAddress"
	busPath             = dbus.ObjectPath("/org/a11y/bus")
	registryName        = "org.a11y.atspi.Registry"
	registryPath        = dbus.ObjectPath("/org/a11y/atspi/registry")
	desktopPath         = dbus.ObjectPath("/org/a11y/atspi/accessible/root")
	nullObjectPath      = dbus.ObjectPath("/org/a11y/atspi/null")
	accessibleIface     = "org.a11y.atspi.Accessible"
	componentIface      = "org.a11y.atspi.Component"
	textIface           = "org.a11y.atspi.Text"
	editableTextIface   = "org.a11y.atspi.EditableText"
	valueIface          = "org.a11y.atspi.Value"
	actionIface         = "org.a11y.atspi.Action"
	selectionIface      = "org.a11y.atspi.Selection"
	tableIface          = "org.a11y.atspi.Table"
	documentIface       = "org.a11y.atspi.Document"
	cacheIface          = "org.a11y.atspi.Cache"
	cachePath           = dbus.ObjectPath("/org/a11y/atspi/cache")
	propertiesIface     = "org.freedesktop.DBus.Properties"
	defaultMaxDepth     = 32
	defaultMaxNodes     = 10000
	defaultMaxText      = 4096
	defaultEventBuffer  = 64
	maxApplications     = 1024
	cacheTTL            = 250 * time.Millisecond
	eventCoalesceWindow = 10 * time.Millisecond
	absMaxDepth         = 64
	absMaxNodes         = 100000
	absMaxText          = 1 << 20
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

type objectRef struct {
	BusName    string
	ObjectPath dbus.ObjectPath
}

func (r objectRef) idAt(generation uint64) NodeID {
	return NodeID{BusName: r.BusName, ObjectPath: string(r.ObjectPath), Generation: generation}
}

func (r objectRef) null() bool {
	return r.ObjectPath == "" || r.ObjectPath == nullObjectPath || r.BusName == ""
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
	ID          NodeID            `json:"id"`
	Parent      NodeID            `json:"parent,omitempty"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Role        string            `json:"role,omitempty"`
	RoleID      uint32            `json:"roleId,omitempty"`
	Interfaces  []string          `json:"interfaces,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	States      []string          `json:"states,omitempty"`
	Text        string            `json:"text,omitempty"`
	Bounds      Rect              `json:"bounds"`
	HasBounds   bool              `json:"hasBounds"`
	ChildCount  int               `json:"childCount"`
	Children    []NodeID          `json:"children,omitempty"`
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

// Action describes one optional AT-SPI action exposed by an object.
type Action struct {
	Index       int32  `json:"index"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	KeyBinding  string `json:"keyBinding,omitempty"`
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
	MaxDepth     int  `json:"maxDepth,omitempty"`
	MaxNodes     int  `json:"maxNodes,omitempty"`
	MaxTextBytes int  `json:"maxTextBytes,omitempty"`
	VisibleOnly  bool `json:"visibleOnly,omitempty"`
	// AllowSensitive disables the default redaction of AT-SPI sensitive and
	// protected text/value attributes. Use only for an explicit trusted flow.
	AllowSensitive bool `json:"allowSensitive,omitempty"`
	// AllowDesktopRoot explicitly opts into a bounded whole-desktop tree. It
	// is false by default so Snapshot and Find cannot accidentally traverse
	// every application when a caller omitted scope.
	AllowDesktopRoot bool `json:"allowDesktopRoot,omitempty"`
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
	if o.MaxDepth > absMaxDepth {
		o.MaxDepth = absMaxDepth
	}
	if o.MaxNodes > absMaxNodes {
		o.MaxNodes = absMaxNodes
	}
	if o.MaxTextBytes > absMaxText {
		o.MaxTextBytes = absMaxText
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

// ApplicationFilter selects an application root without relying on a window
// title. Empty fields are ignored; PID is an exact process match.
type ApplicationFilter struct {
	Name        string `json:"name,omitempty"`
	PID         int32  `json:"pid,omitempty"`
	Bus         string `json:"busName,omitempty"`
	WindowID    string `json:"windowId,omitempty"`
	WindowTitle string `json:"windowTitle,omitempty"`
}

// Event is an invalidation-oriented AT-SPI signal. Signals are hints: callers
// should refresh a Snapshot before acting on a node because objects can be
// destroyed between notification and query.
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
	Root       Node      `json:"root"`
	Nodes      []Node    `json:"nodes"`
	Truncated  bool      `json:"truncated"`
	Warnings   []string  `json:"warnings,omitempty"`
	Generation uint64    `json:"generation"`
	CapturedAt time.Time `json:"capturedAt"`
	Source     string    `json:"source,omitempty"`
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
	InvokeActionByName(context.Context, NodeID, string) error
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

// ValueController exposes typed Value interface operations.
type ValueController interface {
	SetCurrentValue(context.Context, NodeID, float64) error
	SetValue(context.Context, NodeID, float64) error
}

// TextController exposes typed Text and EditableText operations. Providers
// may return ErrUnsupported for operations not implemented by an object.
type TextController interface {
	SetTextContents(context.Context, NodeID, string) error
	ReplaceText(context.Context, NodeID, int32, int32, string) error
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
	DeselectAll(context.Context, NodeID) error
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

// Short aliases retained for callers that prefer the AT-SPI terminology.
const (
	CoordScreen = CoordTypeScreen
	CoordWindow = CoordTypeWindow
	CoordParent = CoordTypeParent
)

// WindowTarget describes an authoritative Perfuncted window used to resolve
// an AT-SPI top-level window/dialog/frame.
type WindowTarget struct {
	ID      string
	Title   string
	PID     int32
	AppID   string
	Bounds  Rect
	Active  bool
	Focused bool
}

// WindowScope is the correlated accessibility subtree root and evidence used
// to choose it. A root is never silently substituted with an application root.
type WindowScope struct {
	WindowID   string      `json:"windowId"`
	Title      string      `json:"title,omitempty"`
	Root       NodeID      `json:"root"`
	Candidates []Candidate `json:"candidates,omitempty"`
	Evidence   []string    `json:"evidence,omitempty"`
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
func OpenRuntime(rt env.Runtime) (Backend, error) {
	return openRuntime(rt, 1)
}

func openRuntime(rt env.Runtime, generation uint64) (Backend, error) {
	if generation == 0 {
		generation = 1
	}
	// Accessibility is session-scoped. Unlike generic D-Bus helpers, never
	// fall back to the caller's host bus when the target runtime omitted its
	// address; doing so could expose or act on another desktop session.
	busAddress := strings.TrimSpace(rt.Get("DBUS_SESSION_BUS_ADDRESS"))
	if busAddress == "" {
		return nil, fmt.Errorf("accessibility: missing target session bus address")
	}
	session, err := dbusutil.SessionBusAddress(busAddress)
	if err != nil {
		return nil, fmt.Errorf("accessibility: connect to session bus: %w", err)
	}
	var address string
	call := session.Object(busService, busPath).Call(busAddressMethod, 0)
	if storeErr := call.Store(&address); storeErr != nil {
		_ = session.Close()
		return nil, fmt.Errorf("accessibility: get accessibility bus address: %w", storeErr)
	}
	if strings.TrimSpace(address) == "" {
		_ = session.Close()
		return nil, fmt.Errorf("accessibility: empty bus address")
	}
	access, err := dbus.Dial(address)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("accessibility: dial accessibility bus: %w", err)
	}
	if err := access.Auth(nil); err != nil {
		_ = access.Close()
		_ = session.Close()
		return nil, fmt.Errorf("accessibility: authenticate accessibility bus: %w", err)
	}
	if err := access.Hello(); err != nil {
		_ = access.Close()
		_ = session.Close()
		return nil, fmt.Errorf("accessibility: hello accessibility bus: %w", err)
	}
	backend := &dbusBackend{
		runtime: rt, session: session, access: access, generation: generation,
		cache:      make(map[string]cachedSnapshot),
		cacheItems: make(map[NodeID]cacheItem), cacheApps: make(map[string]bool),
		subscribers: make(map[uint64]*eventSubscriber),
	}
	backend.watchDisconnect()
	return backend, nil
}

type dbusBackend struct {
	runtime      env.Runtime
	session      *dbus.Conn
	access       *dbus.Conn
	mu           sync.RWMutex
	generation   uint64
	disconnected bool
	closed       bool
	cache        map[string]cachedSnapshot
	// cacheItems is the optional upstream AT-SPI cache. It is keyed by the
	// complete object reference, so objects from different application buses
	// cannot collide. The local snapshot cache remains a short-lived response
	// optimization; cacheItems is refreshed by Cache.GetItems and signals.
	cacheItems     map[NodeID]cacheItem
	cacheApps      map[string]bool
	eventsMu       sync.Mutex
	subscribers    map[uint64]*eventSubscriber
	nextSubscriber uint64
	eventCancel    context.CancelFunc
	eventDone      chan struct{}
	// callOverride is used only by deterministic package tests to exercise
	// protocol and error handling without a host D-Bus daemon.
	callOverride func(context.Context, NodeID, string, []any) (any, error)
}

type eventSubscriber struct {
	out     chan Event
	dropped uint64
}

type cachedSnapshot struct {
	at       time.Time
	snapshot Snapshot
}

// cacheObjectRef and cacheItem mirror the current Cache.GetItems wire shape:
// a((so)(so)(so)iiassusau). Keep these private so protocol details do not leak
// into the public API. Older providers may return the historical signature;
// a failed decode simply falls back to direct reads.
type cacheObjectRef struct {
	BusName    string
	ObjectPath dbus.ObjectPath
}

type cacheItem struct {
	Object      cacheObjectRef
	Application cacheObjectRef
	Parent      cacheObjectRef
	Index       int32
	ChildCount  int32
	Interfaces  []string
	Name        string
	Role        uint32
	Description string
	States      []uint32
}

func (item cacheItem) nodeID() NodeID {
	return NodeID{BusName: item.Object.BusName, ObjectPath: string(item.Object.ObjectPath), Generation: 1}
}

func (item cacheItem) nodeIDAt(generation uint64) NodeID {
	return NodeID{BusName: item.Object.BusName, ObjectPath: string(item.Object.ObjectPath), Generation: generation}
}

func (b *dbusBackend) SupportedOperations() []string {
	return []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events", "outline", "invoke-action", "invoke-action-by-name", "invoke-default-action", "grab-focus", "scroll", "scroll-to-point", "set-position", "set-size", "set-extents", "set-current-value", "set-value", "set-text-contents", "replace-text", "insert-text", "delete-text", "copy-text", "cut-text", "paste-text", "set-caret", "set-text-selection", "add-text-selection", "remove-text-selection", "set-document-text-selections", "select-child", "deselect-child", "select-all", "clear-selection", "deselect-all", "deselect-selected-child", "select-row", "deselect-row", "select-column", "deselect-column", "window-root", "reopen"}
}

// Generation returns the current invalidation generation. It changes when an
// AT-SPI signal is observed or Invalidate is called explicitly.
func (b *dbusBackend) Generation() uint64 {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.generation
}

// Invalidate advances the generation and clears both the bounded snapshot
// cache and the upstream cache metadata. AT-SPI cache entries are generation
// scoped too: a non-cache signal may have changed a cached name, role, or
// state, so retaining entries across the transition would silently reuse
// stale metadata. Cache signals preserve and update the upstream state in
// prepareEvent after this transition.
func (b *dbusBackend) Invalidate(_ NodeID) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.generation++
	b.cache, b.cacheItems, b.cacheApps = nil, nil, nil
	b.mu.Unlock()
}

func (b *dbusBackend) Close() error {
	var errs []error
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.disconnected = true
	b.generation++
	access, session := b.access, b.session
	b.access, b.session = nil, nil
	b.cache, b.cacheItems, b.cacheApps = nil, nil, nil
	b.mu.Unlock()
	b.stopEvents()
	if access != nil {
		errs = append(errs, access.Close())
	}
	if session != nil {
		errs = append(errs, session.Close())
	}
	return errors.Join(errs...)
}

func (b *dbusBackend) object(id NodeID) (dbus.BusObject, error) {
	if b == nil {
		return nil, ErrDisconnected
	}
	if err := b.validateHandle(id); err != nil {
		return nil, err
	}
	b.mu.RLock()
	access := b.access
	b.mu.RUnlock()
	if access == nil {
		return nil, ErrDisconnected
	}
	return access.Object(id.BusName, dbus.ObjectPath(id.ObjectPath)), nil
}

func (b *dbusBackend) validateHandle(id NodeID) error {
	if b == nil {
		return ErrDisconnected
	}
	b.mu.RLock()
	disconnected, closed, generation := b.disconnected, b.closed, b.generation
	b.mu.RUnlock()
	if disconnected || closed {
		return ErrDisconnected
	}
	if !id.valid() {
		return ErrStaleNode
	}
	if id.Generation != generation {
		return fmt.Errorf("%w: handle generation %d, current generation %d", ErrStaleNode, id.Generation, generation)
	}
	return nil
}

func (b *dbusBackend) desktop() NodeID {
	return NodeID{BusName: registryName, ObjectPath: string(desktopPath), Generation: b.Generation()}
}

func (b *dbusBackend) refID(ref objectRef) NodeID {
	if b == nil {
		return NodeID{}
	}
	return ref.idAt(b.Generation())
}

func (b *dbusBackend) tagRefs(refs []objectRef) []objectRef {
	return refs
}

func (b *dbusBackend) Applications(ctx context.Context) ([]Application, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	refs, err := b.children(ctx, b.desktop())
	if err != nil {
		return nil, err
	}
	if len(refs) > maxApplications {
		refs = refs[:maxApplications]
	}
	apps := make([]Application, 0, len(refs))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Cache.GetItems is an optional optimization. Providers that do not
		// expose it continue through the direct Accessible interface path.
		_ = b.loadCache(ctx, ref.BusName)
		node, err := b.readNode(ctx, b.refID(ref), NodeID{}, defaultMaxText, false)
		if err != nil {
			continue
		}
		app := Application{Node: node}
		app.PID, _ = b.connectionPID(ctx, ref.BusName)
		_ = b.property(ctx, b.refID(ref), "org.a11y.atspi.Application", "ToolkitName", &app.ToolkitName)
		_ = b.property(ctx, b.refID(ref), "org.a11y.atspi.Application", "ToolkitVersion", &app.ToolkitVersion)
		apps = append(apps, app)
	}
	return apps, nil
}

// FindApplication returns exactly one application matching filter. Name is
// matched against the accessible name and PID/Bus are exact constraints.
func (b *dbusBackend) FindApplication(ctx context.Context, filter ApplicationFilter) (Application, error) { //nolint:gocyclo // selector matching deliberately keeps each scope constraint visible.
	apps, err := b.Applications(ctx)
	if err != nil {
		return Application{}, err
	}
	want := strings.ToLower(strings.TrimSpace(filter.Name))
	matches := make([]Application, 0, 1)
	for _, app := range apps {
		if filter.PID != 0 && app.PID != filter.PID {
			continue
		}
		if filter.Bus != "" && app.ID.BusName != filter.Bus {
			continue
		}
		if want != "" && !strings.Contains(strings.ToLower(app.Name), want) {
			continue
		}
		if windowTitle := strings.ToLower(strings.TrimSpace(filter.WindowTitle)); windowTitle != "" &&
			!strings.Contains(strings.ToLower(app.Name), windowTitle) &&
			!strings.Contains(strings.ToLower(app.Description), windowTitle) {
			continue
		}
		if filter.WindowID != "" && app.ID.ObjectPath != filter.WindowID {
			continue
		}
		matches = append(matches, app)
	}
	switch len(matches) {
	case 0:
		return Application{}, ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return Application{}, fmt.Errorf("%w: %d applications matched", ErrAmbiguous, len(matches))
	}
}

func (b *dbusBackend) connectionPID(ctx context.Context, busName string) (int32, error) {
	if strings.TrimSpace(busName) == "" || b == nil {
		return 0, nil
	}
	b.mu.RLock()
	access := b.access
	b.mu.RUnlock()
	if access == nil {
		return 0, nil
	}
	var pid uint32
	if err := access.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").CallWithContext(ctx, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, busName).Store(&pid); err != nil {
		return 0, err
	}
	if pid > 1<<31-1 {
		return 0, fmt.Errorf("accessibility: pid %d out of range", pid)
	}
	return int32(pid), nil
}

// Events subscribes to AT-SPI object/window notifications. The protocol is
// intentionally treated as an invalidation stream: unknown payload fields are
// retained as strings and dropped events never make a node authoritative.
func (b *dbusBackend) Events(ctx context.Context, opts EventOptions) (<-chan Event, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	if b == nil {
		return nil, ErrDisconnected
	}
	if err := b.connected(); err != nil {
		return nil, err
	}
	opts = opts.normalized()
	out := make(chan Event, opts.Buffer)
	b.eventsMu.Lock()
	b.nextSubscriber++
	id := b.nextSubscriber
	b.subscribers[id] = &eventSubscriber{out: out}
	start := b.eventCancel == nil
	b.eventsMu.Unlock()
	if start {
		if err := b.startEvents(ctx); err != nil {
			b.eventsMu.Lock()
			delete(b.subscribers, id)
			b.eventsMu.Unlock()
			close(out)
			return nil, err
		}
	}
	go b.watchSubscriber(ctx, id)
	return out, nil
}

func registerEvents(ctx context.Context, access *dbus.Conn) (dbus.BusObject, []string) {
	registered := []string{
		"object:property-change",
		"object:state-changed",
		"object:children-changed",
		"object:text-changed",
		"object:visible-data-changed",
		"focus:focus",
		"window:activate",
		"window:deactivate",
		"window:create",
		"window:destroy",
	}
	registry := access.Object(registryName, registryPath)
	unique := ""
	if names := access.Names(); len(names) > 0 {
		unique = names[0]
	}
	for _, eventType := range registered {
		// Some registry versions reject event families they do not emit. Keep
		// the successful registrations and continue with the signal stream.
		_ = registry.CallWithContext(ctx, registryName+".RegisterEvent", 0, eventType, []string{}, unique).Err
	}
	return registry, registered
}

func subscribeEventMatches(ctx context.Context, access *dbus.Conn) ([][]dbus.MatchOption, error) {
	matches := [][]dbus.MatchOption{
		{dbus.WithMatchInterface("org.a11y.atspi.Event.Object")},
		{dbus.WithMatchInterface("org.a11y.atspi.Event.Focus")},
		{dbus.WithMatchInterface("org.a11y.atspi.Event.Window")},
		{dbus.WithMatchInterface(cacheIface)},
	}
	for _, match := range matches {
		if err := access.AddMatchSignalContext(ctx, match...); err != nil {
			for _, installed := range matches {
				_ = access.RemoveMatchSignal(installed...)
			}
			return nil, fmt.Errorf("accessibility: subscribe events: %w", err)
		}
	}
	return matches, nil
}

type eventRegistrar interface {
	CallWithContext(context.Context, string, dbus.Flags, ...any) *dbus.Call
}

func deregisterEvents(ctx context.Context, registry eventRegistrar, registered []string) {
	for _, eventType := range registered {
		_ = registry.CallWithContext(ctx, registryName+".DeregisterEvent", 0, eventType).Err
	}
}

func coalesceEvent(lastKey *string, lastAt *time.Time, event Event) bool {
	if lastKey == nil || lastAt == nil {
		return false
	}
	key := event.Kind + "\x00" + event.Node.BusName + "\x00" + event.Node.ObjectPath + "\x00" + event.Property
	if key == *lastKey && !lastAt.IsZero() && event.Timestamp.Sub(*lastAt) <= eventCoalesceWindow {
		return true
	}
	*lastKey, *lastAt = key, event.Timestamp
	return false
}

func signalEvent(sig *dbus.Signal) Event {
	event := Event{Timestamp: time.Now()}
	if sig == nil {
		return event
	}
	event.Kind = sig.Name
	event.Node = NodeID{BusName: sig.Sender, ObjectPath: string(sig.Path)}
	if len(sig.Body) > 0 {
		if property, ok := sig.Body[0].(string); ok {
			event.Property = property
		}
	}
	if len(sig.Body) > 1 {
		event.Value = fmt.Sprint(sig.Body[1])
		if sensitiveEventProperty(event.Property) {
			event.Value = ""
		}
	}
	return event
}

func sensitiveEventProperty(property string) bool {
	lower := strings.ToLower(strings.TrimSpace(property))
	return lower == "value" || strings.Contains(lower, "text") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "protected")
}

func (b *dbusBackend) Snapshot(ctx context.Context, root NodeID, opts SnapshotOptions) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, errors.New("accessibility: nil context")
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if err := b.connected(); err != nil {
		return Snapshot{}, err
	}
	opts = opts.normalized()
	if root == (NodeID{}) {
		if !opts.AllowDesktopRoot {
			return Snapshot{}, fmt.Errorf("%w: Snapshot requires an application, window, or root handle", ErrScope)
		}
		root = b.desktop()
	} else if !root.valid() {
		return Snapshot{}, fmt.Errorf("%w: invalid snapshot root", ErrScope)
	}
	if root.BusName != registryName {
		_ = b.loadCache(ctx, root.BusName)
	}
	key := snapshotKey(root, opts)
	now := time.Now()
	b.mu.RLock()
	if entry, ok := b.cache[key]; ok && now.Sub(entry.at) < cacheTTL {
		snapshot := cloneSnapshot(entry.snapshot)
		b.mu.RUnlock()
		return snapshot, nil
	}
	b.mu.RUnlock()
	walker := snapshotWalker{
		backend: b,
		opts:    opts,
		snapshot: Snapshot{
			Nodes: make([]Node, 0, min(opts.MaxNodes, 1024)),
		},
		seen: make(map[NodeID]struct{}, min(opts.MaxNodes, 1024)),
	}
	rootNode, err := walker.walk(ctx, root, NodeID{}, 0)
	if err != nil && (ctx.Err() != nil || len(walker.snapshot.Nodes) == 0) {
		return Snapshot{}, err
	}
	walker.snapshot.Root = rootNode
	walker.snapshot.CapturedAt = now
	walker.snapshot.Generation = b.Generation()
	walker.snapshot.Source = "at-spi"
	b.mu.Lock()
	if b.cache == nil {
		b.cache = make(map[string]cachedSnapshot)
	}
	b.cache[key] = cachedSnapshot{at: now, snapshot: cloneSnapshot(walker.snapshot)}
	b.mu.Unlock()
	return walker.snapshot, nil
}

func snapshotKey(root NodeID, opts SnapshotOptions) string {
	return root.BusName + "\x00" + root.ObjectPath + "\x00" + strconv.Itoa(opts.MaxDepth) + "\x00" + strconv.Itoa(opts.MaxNodes) + "\x00" + strconv.Itoa(opts.MaxTextBytes) + "\x00" + strconv.FormatBool(opts.VisibleOnly) + "\x00" + strconv.FormatBool(opts.AllowSensitive)
}

func cloneSnapshot(in Snapshot) Snapshot {
	out := in
	out.Nodes = append([]Node(nil), in.Nodes...)
	out.Warnings = append([]string(nil), in.Warnings...)
	for i := range out.Nodes {
		out.Nodes[i].Interfaces = append([]string(nil), in.Nodes[i].Interfaces...)
		out.Nodes[i].States = append([]string(nil), in.Nodes[i].States...)
		out.Nodes[i].Children = append([]NodeID(nil), in.Nodes[i].Children...)
		out.Nodes[i].Warnings = append([]string(nil), in.Nodes[i].Warnings...)
		out.Nodes[i].Actions = append([]Action(nil), in.Nodes[i].Actions...)
		if in.Nodes[i].Value != nil {
			value := *in.Nodes[i].Value
			out.Nodes[i].Value = &value
		}
		if in.Nodes[i].Selection != nil {
			selection := *in.Nodes[i].Selection
			out.Nodes[i].Selection = &selection
		}
		if in.Nodes[i].Table != nil {
			table := *in.Nodes[i].Table
			out.Nodes[i].Table = &table
		}
		if in.Nodes[i].Document != nil {
			document := *in.Nodes[i].Document
			out.Nodes[i].Document = &document
		}
		if in.Nodes[i].Relations != nil {
			out.Nodes[i].Relations = make(map[string][]NodeID, len(in.Nodes[i].Relations))
			for key, values := range in.Nodes[i].Relations {
				out.Nodes[i].Relations[key] = append([]NodeID(nil), values...)
			}
		}
		if in.Nodes[i].Attributes != nil {
			out.Nodes[i].Attributes = make(map[string]string, len(in.Nodes[i].Attributes))
			for key, value := range in.Nodes[i].Attributes {
				out.Nodes[i].Attributes[key] = value
			}
		}
	}
	if len(out.Nodes) > 0 {
		out.Root = out.Nodes[0]
	}
	return out
}

type snapshotWalker struct {
	backend  snapshotBackend
	opts     SnapshotOptions
	snapshot Snapshot
	seen     map[NodeID]struct{}
}

type snapshotBackend interface {
	readNode(context.Context, NodeID, NodeID, int, bool) (Node, error)
	children(context.Context, NodeID) ([]objectRef, error)
	refID(objectRef) NodeID
}

func (w *snapshotWalker) walk(ctx context.Context, id, parent NodeID, depth int) (Node, error) {
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
	if depth > w.opts.MaxDepth {
		w.snapshot.Truncated = true
		return Node{}, fmt.Errorf("accessibility: max depth %d reached", w.opts.MaxDepth)
	}
	if len(w.snapshot.Nodes) >= w.opts.MaxNodes {
		w.snapshot.Truncated = true
		return Node{}, fmt.Errorf("accessibility: max nodes %d reached", w.opts.MaxNodes)
	}
	if _, ok := w.seen[id]; ok {
		return Node{}, nil
	}
	w.seen[id] = struct{}{}
	node, err := w.backend.readNode(ctx, id, parent, w.opts.MaxTextBytes, w.opts.AllowSensitive)
	if err != nil {
		return Node{}, err
	}
	if w.opts.VisibleOnly && depth > 0 && !node.Visible && !node.Showing {
		return Node{}, nil
	}
	if len(node.Warnings) > 0 {
		w.snapshot.Warnings = append(w.snapshot.Warnings, node.Warnings...)
	}
	nodeIndex := len(w.snapshot.Nodes)
	w.snapshot.Nodes = append(w.snapshot.Nodes, node)
	if node.ChildCount > 0 {
		w.walkChildren(ctx, &node, id, depth)
	}
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
	w.snapshot.Nodes[nodeIndex] = node
	return node, nil
}

func (w *snapshotWalker) walkChildren(ctx context.Context, node *Node, id NodeID, depth int) {
	children, err := w.backend.children(ctx, id)
	if err != nil {
		w.snapshot.Warnings = append(w.snapshot.Warnings, fmt.Sprintf("%s: %v", id.ObjectPath, err))
		return
	}
	for _, childRef := range children {
		if len(w.snapshot.Nodes) >= w.opts.MaxNodes {
			w.snapshot.Truncated = true
			return
		}
		childID := w.backend.refID(childRef)
		child, childErr := w.walk(ctx, childID, id, depth+1)
		if childErr != nil {
			if ctx.Err() != nil {
				return
			}
			w.snapshot.Warnings = append(w.snapshot.Warnings, childErr.Error())
			continue
		}
		if child.ID.valid() {
			node.Children = append(node.Children, child.ID)
		}
	}
}

func (b *dbusBackend) Find(ctx context.Context, root NodeID, query Query, opts SnapshotOptions) ([]Node, error) {
	snapshot, err := b.Snapshot(ctx, root, opts)
	if err != nil {
		return nil, err
	}
	wantName, wantRole, wantText := strings.ToLower(strings.TrimSpace(query.Name)), strings.ToLower(strings.TrimSpace(query.Role)), strings.ToLower(strings.TrimSpace(query.Text))
	result := make([]Node, 0)
	for _, node := range snapshot.Nodes {
		if wantName != "" && !strings.Contains(strings.ToLower(node.Name), wantName) {
			continue
		}
		if wantRole != "" && !strings.Contains(strings.ToLower(node.Role), wantRole) {
			continue
		}
		if wantText != "" && !strings.Contains(strings.ToLower(node.Text), wantText) {
			continue
		}
		if !matchesStates(node.States, query.States) || !matchesAttributes(node.Attributes, query.Attributes) {
			continue
		}
		result = append(result, node)
	}
	return result, nil
}

func matchesStates(have, want []string) bool {
	for _, requested := range want {
		found := false
		for _, state := range have {
			if strings.EqualFold(strings.TrimSpace(requested), state) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func matchesAttributes(have, want map[string]string) bool {
	for key, expected := range want {
		actual, ok := "", false
		for haveKey, haveValue := range have {
			if strings.EqualFold(haveKey, key) {
				actual, ok = haveValue, true
				break
			}
		}
		if !ok || !strings.EqualFold(actual, expected) {
			return false
		}
	}
	return true
}

func (b *dbusBackend) Focused(ctx context.Context, opts SnapshotOptions) (Node, error) {
	if ctx == nil {
		return Node{}, errors.New("accessibility: nil context")
	}
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
	opts.AllowDesktopRoot = true
	snapshot, err := b.Snapshot(ctx, NodeID{}, opts)
	if err != nil {
		return Node{}, err
	}
	for _, node := range snapshot.Nodes {
		if node.Focused {
			return node, nil
		}
	}
	return Node{}, ErrNotFound
}

func (b *dbusBackend) AtPoint(ctx context.Context, x, y int) (Node, error) {
	if ctx == nil {
		return Node{}, errors.New("accessibility: nil context")
	}
	if x < -2147483648 || x > 2147483647 || y < -2147483648 || y > 2147483647 {
		return Node{}, errors.New("accessibility: point coordinates out of range")
	}
	obj, err := b.object(b.desktop())
	if err != nil {
		return Node{}, err
	}
	var ref objectRef
	if err := obj.CallWithContext(ctx, componentIface+".GetAccessibleAtPoint", 0, int32(x), int32(y), uint32(0)).Store(&ref); err != nil {
		return Node{}, fmt.Errorf("accessibility: point query: %w", err)
	}
	if ref.null() {
		return Node{}, ErrNotFound
	}
	return b.readNode(ctx, b.refID(ref), NodeID{}, defaultMaxText, false)
}

func (b *dbusBackend) children(ctx context.Context, id NodeID) ([]objectRef, error) {
	if cached := b.cachedChildren(id); cached != nil {
		return cached, nil
	}
	obj, err := b.object(id)
	if err != nil {
		return nil, err
	}
	var refs []objectRef
	if err := obj.CallWithContext(ctx, accessibleIface+".GetChildren", 0).Store(&refs); err != nil {
		return nil, fmt.Errorf("accessibility: children %s: %w", id.ObjectPath, err)
	}
	return b.tagRefs(refs), nil
}

func (b *dbusBackend) cachedChildren(id NodeID) []objectRef {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.cacheItems) == 0 || !b.cacheApps[id.BusName] {
		return nil
	}
	type indexed struct {
		index int32
		ref   objectRef
	}
	items := make([]indexed, 0)
	for key, item := range b.cacheItems {
		if key.Generation != id.Generation {
			continue
		}
		if item.Parent.BusName == id.BusName && string(item.Parent.ObjectPath) == id.ObjectPath {
			items = append(items, indexed{index: item.Index, ref: objectRef{BusName: item.Object.BusName, ObjectPath: item.Object.ObjectPath}})
		}
	}
	if len(items) == 0 {
		return nil
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].index != items[j].index {
			return items[i].index < items[j].index
		}
		return items[i].ref.ObjectPath < items[j].ref.ObjectPath
	})
	refs := make([]objectRef, 0, len(items))
	for _, item := range items {
		refs = append(refs, item.ref)
	}
	return refs
}

func (b *dbusBackend) loadCache(ctx context.Context, busName string) error {
	if b == nil || strings.TrimSpace(busName) == "" || busName == registryName {
		return ErrUnsupported
	}
	b.mu.RLock()
	if b.cacheApps[busName] && b.cacheItems != nil {
		b.mu.RUnlock()
		return nil
	}
	access := b.access
	b.mu.RUnlock()
	if access == nil {
		return errors.New("accessibility: bus is closed")
	}
	var items []cacheItem
	call := access.Object(busName, cachePath).CallWithContext(ctx, cacheIface+".GetItems", 0)
	if err := call.Store(&items); err != nil {
		return fmt.Errorf("accessibility: cache get items for %s: %w", busName, err)
	}
	b.mu.Lock()
	generation := b.generation
	if b.cacheItems == nil {
		b.cacheItems = make(map[NodeID]cacheItem)
	}
	for _, item := range items {
		id := item.nodeIDAt(generation)
		if id.valid() {
			b.cacheItems[id] = item
		}
	}
	if b.cacheApps == nil {
		b.cacheApps = make(map[string]bool)
	}
	b.cacheApps[busName] = true
	b.mu.Unlock()
	return nil
}

func (b *dbusBackend) applyCacheSignal(sig *dbus.Signal) {
	if b == nil || sig == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cacheItems == nil {
		b.cacheItems = make(map[NodeID]cacheItem)
	}
	switch {
	case strings.HasSuffix(sig.Name, "Cache:AddAccessible"):
		b.applyCacheAddLocked(sig.Body)
	case strings.HasSuffix(sig.Name, "Cache:RemoveAccessible"):
		b.applyCacheRemoveLocked(sig.Body)
	}
	b.cache = nil
}

func (b *dbusBackend) applyCacheAddLocked(body []any) {
	if len(body) == 0 {
		return
	}
	var item cacheItem
	switch value := body[0].(type) {
	case cacheItem:
		item = value
	case *cacheItem:
		if value == nil {
			b.cacheItems, b.cacheApps = nil, nil
			return
		}
		item = *value
	default:
		// A provider with an older wire signature may still emit a valid
		// cache signal. Invalidate the local item map and let the next
		// snapshot reload it rather than guessing at its shape.
		b.cacheItems, b.cacheApps = nil, nil
		return
	}
	b.cacheItems[item.nodeIDAt(b.generation)] = item
	if b.cacheApps == nil {
		b.cacheApps = make(map[string]bool)
	}
	b.cacheApps[item.Object.BusName] = true
}

func (b *dbusBackend) applyCacheRemoveLocked(body []any) {
	if len(body) == 0 {
		return
	}
	ref, ok := body[0].(cacheObjectRef)
	if !ok {
		b.cacheItems, b.cacheApps = nil, nil
		return
	}
	for id := range b.cacheItems {
		if id.BusName == ref.BusName && id.ObjectPath == string(ref.ObjectPath) {
			delete(b.cacheItems, id)
		}
	}
}

func (b *dbusBackend) readNode(ctx context.Context, id, parent NodeID, maxText int, allowSensitive bool) (Node, error) {
	if _, err := b.object(id); err != nil {
		return Node{}, err
	}
	node := Node{ID: id, Parent: parent}
	if item, ok := b.cachedItem(id); ok {
		node.Name = item.Name
		node.Description = item.Description
		node.ChildCount = int(maxInt32(item.ChildCount))
		node.RoleID = item.Role
		node.Role = roleName(item.Role)
		node.Interfaces = append([]string(nil), item.Interfaces...)
		b.applyStates(item.States, &node)
	} else {
		_ = b.property(ctx, id, accessibleIface, "Name", &node.Name)
		_ = b.property(ctx, id, accessibleIface, "Description", &node.Description)
		b.readNodeRoleAndCount(ctx, id, &node)
		node.Interfaces = b.readNodeInterfaces(ctx, id)
		b.readNodeStates(ctx, id, &node)
	}
	b.readNodeComponent(ctx, id, node.Interfaces, &node)
	b.readNodeText(ctx, id, node.Interfaces, maxText, &node)
	b.readNodeAttributes(ctx, id, &node)
	b.readNodeOptional(ctx, id, node.Interfaces, &node)
	if !allowSensitive && hasSensitiveState(node.States) {
		redactSensitiveNode(&node)
	}
	return node, nil
}

func (b *dbusBackend) cachedItem(id NodeID) (cacheItem, bool) {
	if b == nil {
		return cacheItem{}, false
	}
	b.mu.RLock()
	item, ok := b.cacheItems[id]
	b.mu.RUnlock()
	return item, ok
}

func maxInt32(value int32) int32 {
	if value < 0 {
		return 0
	}
	return value
}

func redactSensitiveNode(node *Node) {
	if node == nil {
		return
	}
	node.Redacted = true
	node.Text = ""
	// Value.Current can itself be sensitive (for example a password or
	// protected spin control). Mutation permission is intentionally separate
	// from reading this field.
	node.Value = nil
	for key := range node.Attributes {
		lower := strings.ToLower(key)
		if lower == "value" || strings.Contains(lower, "text") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "protected") {
			delete(node.Attributes, key)
		}
	}
}

func hasSensitiveState(states []string) bool {
	for _, state := range states {
		if strings.EqualFold(state, "sensitive") || strings.EqualFold(state, "protected") {
			return true
		}
	}
	return false
}

func (b *dbusBackend) readNodeRoleAndCount(ctx context.Context, id NodeID, node *Node) {
	var count int32
	if err := b.property(ctx, id, accessibleIface, "ChildCount", &count); err == nil && count > 0 {
		node.ChildCount = int(count)
	}
	var role uint32
	if err := b.call(ctx, id, accessibleIface+".GetRole", nil, &role); err == nil {
		node.RoleID = role
		node.Role = roleName(role)
	}
}

func (b *dbusBackend) readNodeInterfaces(ctx context.Context, id NodeID) []string {
	var interfaces []string
	if err := b.call(ctx, id, accessibleIface+".GetInterfaces", nil, &interfaces); err != nil {
		return nil
	}
	return interfaces
}

func (b *dbusBackend) readNodeStates(ctx context.Context, id NodeID, node *Node) {
	var states []uint32
	if err := b.call(ctx, id, accessibleIface+".GetState", nil, &states); err != nil {
		return
	}
	b.applyStates(states, node)
}

func (b *dbusBackend) applyStates(states []uint32, node *Node) {
	for _, state := range states {
		if name := stateName(state); name != "" {
			node.States = append(node.States, name)
		}
		switch state {
		case 8:
			node.Enabled = true
		case 12:
			node.Focused = true
		case 25:
			node.Showing = true
		case 30:
			node.Visible = true
		}
	}
}

func (b *dbusBackend) readNodeComponent(ctx context.Context, id NodeID, interfaces []string, node *Node) {
	if !contains(interfaces, componentIface) {
		return
	}
	var extents struct{ X, Y, Width, Height int32 }
	if err := b.call(ctx, id, componentIface+".GetExtents", []any{uint32(0)}, &extents); err != nil {
		return
	}
	node.Bounds = Rect{X: int(extents.X), Y: int(extents.Y), Width: int(extents.Width), Height: int(extents.Height)}
	node.HasBounds = true
}

func (b *dbusBackend) readNodeText(ctx context.Context, id NodeID, interfaces []string, maxText int, node *Node) {
	if !contains(interfaces, textIface) {
		return
	}
	var chars int32
	if err := b.property(ctx, id, textIface, "CharacterCount", &chars); err != nil || chars <= 0 {
		return
	}
	if maxText <= 0 {
		maxText = defaultMaxText
	}
	if chars > int32(maxText) {
		chars = int32(maxText)
	}
	_ = b.call(ctx, id, textIface+".GetText", []any{int32(0), chars}, &node.Text)
}

func (b *dbusBackend) readNodeAttributes(ctx context.Context, id NodeID, node *Node) {
	var attrs map[string]string
	if err := b.call(ctx, id, accessibleIface+".GetAttributes", nil, &attrs); err == nil {
		node.Attributes = attrs
	}
}

// readNodeOptional performs best-effort reads for the optional AT-SPI
// interfaces advertised by GetInterfaces. Every field is independent: a
// provider that implements only part of an interface must not make the whole
// node unreadable.
func (b *dbusBackend) readNodeOptional(ctx context.Context, id NodeID, interfaces []string, node *Node) { //nolint:gocyclo // each optional interface is intentionally isolated.
	if contains(interfaces, valueIface) {
		value := &ValueInfo{}
		if err := b.propertyFloat(ctx, id, valueIface, "CurrentValue", &value.Current); err == nil {
			for _, field := range []struct {
				name   string
				target *float64
			}{{"MinimumValue", &value.Minimum}, {"MaximumValue", &value.Maximum}, {"MinimumIncrement", &value.MinimumIncrement}} {
				if optionalErr := b.propertyFloat(ctx, id, valueIface, field.name, field.target); optionalErr != nil {
					node.Warnings = append(node.Warnings, fmt.Sprintf("%s.%s: %v", valueIface, field.name, optionalErr))
				}
			}
			node.Value = value
		} else {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s.CurrentValue: %v", valueIface, err))
		}
	}
	if contains(interfaces, actionIface) {
		var wire []struct {
			Name        string
			Description string
			KeyBinding  string
		}
		if err := b.call(ctx, id, actionIface+".GetActions", nil, &wire); err == nil {
			node.Actions = make([]Action, len(wire))
			for i, action := range wire {
				node.Actions[i] = Action{Index: int32(i), Name: action.Name, Description: action.Description, KeyBinding: action.KeyBinding}
			}
		} else {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s.GetActions: %v", actionIface, err))
		}
	}
	if contains(interfaces, selectionIface) {
		var count int32
		if err := b.call(ctx, id, selectionIface+".GetSelectedChildCount", nil, &count); err == nil {
			node.Selection = &SelectionInfo{SelectedChildCount: maxInt32(count)}
		} else {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s.GetSelectedChildCount: %v", selectionIface, err))
		}
	}
	if contains(interfaces, tableIface) {
		var rows, columns int32
		rowsErr := b.property(ctx, id, tableIface, "NRows", &rows)
		columnsErr := b.property(ctx, id, tableIface, "NColumns", &columns)
		if rowsErr == nil || columnsErr == nil {
			node.Table = &TableInfo{Rows: maxInt32(rows), Columns: maxInt32(columns)}
		}
		if rowsErr != nil {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s.NRows: %v", tableIface, rowsErr))
		}
		if columnsErr != nil {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s.NColumns: %v", tableIface, columnsErr))
		}
	}
	if contains(interfaces, documentIface) {
		document := &DocumentInfo{}
		got := false
		if b.property(ctx, id, documentIface, "Locale", &document.Locale) == nil {
			got = true
		}
		if b.property(ctx, id, documentIface, "CurrentPageNumber", &document.CurrentPageNumber) == nil {
			got = true
		}
		if b.property(ctx, id, documentIface, "PageCount", &document.PageCount) == nil {
			got = true
		}
		if got {
			node.Document = document
		}
		if !got {
			node.Warnings = append(node.Warnings, fmt.Sprintf("%s: no readable document properties", documentIface))
		}
	}
	if len(interfaces) > 0 {
		b.readNodeRelations(ctx, id, node)
	}
}

func (b *dbusBackend) readNodeRelations(ctx context.Context, id NodeID, node *Node) {
	var relations []struct {
		RelationType uint32
		Targets      []objectRef
	}
	if err := b.call(ctx, id, accessibleIface+".GetRelationSet", nil, &relations); err != nil {
		node.Warnings = append(node.Warnings, fmt.Sprintf("%s.GetRelationSet: %v", accessibleIface, err))
		return
	}
	for _, relation := range relations {
		name := relationName(relation.RelationType)
		if name == "" || len(relation.Targets) == 0 {
			continue
		}
		if node.Relations == nil {
			node.Relations = make(map[string][]NodeID)
		}
		for _, target := range relation.Targets {
			if !target.null() {
				node.Relations[name] = append(node.Relations[name], b.refID(target))
			}
		}
	}
}

func (b *dbusBackend) propertyFloat(ctx context.Context, id NodeID, iface, name string, out *float64) error {
	var variant dbus.Variant
	if err := b.call(ctx, id, propertiesIface+".Get", []any{iface, name}, &variant); err != nil {
		return err
	}
	switch value := variant.Value().(type) {
	case float64:
		*out = value
	case float32:
		*out = float64(value)
	case int32:
		*out = float64(value)
	case uint32:
		*out = float64(value)
	default:
		return fmt.Errorf("accessibility: property %s.%s has type %T", iface, name, value)
	}
	return nil
}

func (b *dbusBackend) call(ctx context.Context, id NodeID, method string, args []any, out any) error {
	obj, err := b.object(id)
	if err != nil {
		return err
	}
	if args == nil {
		args = []any{}
	}
	return obj.CallWithContext(ctx, method, 0, args...).Store(out)
}

func (b *dbusBackend) property(ctx context.Context, id NodeID, iface, name string, out any) error {
	var variant dbus.Variant
	if err := b.call(ctx, id, propertiesIface+".Get", []any{iface, name}, &variant); err != nil {
		return err
	}
	value := variant.Value()
	switch dst := out.(type) {
	case *string:
		v, ok := value.(string)
		if !ok {
			return fmt.Errorf("accessibility: property %s.%s has type %T", iface, name, value)
		}
		*dst = v
	case *int32:
		v, ok := value.(int32)
		if !ok {
			return fmt.Errorf("accessibility: property %s.%s has type %T", iface, name, value)
		}
		*dst = v
	default:
		return fmt.Errorf("accessibility: unsupported property destination %T", out)
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func roleName(role uint32) string {
	roles := map[uint32]string{7: "check-box", 11: "combo-box", 16: "dialog", 23: "frame", 25: "html-container", 31: "list", 32: "list-item", 39: "panel", 43: "button", 61: "text", 75: "application", 79: "entry", 93: "heading", 98: "link", 99: "list-box", 106: "paragraph", 110: "push-button", 130: "switch"}
	if name, ok := roles[role]; ok {
		return name
	}
	return fmt.Sprintf("role-%d", role)
}

func stateName(state uint32) string {
	states := map[uint32]string{1: "active", 3: "busy", 4: "checked", 5: "collapsed", 7: "editable", 8: "enabled", 9: "expandable", 10: "expanded", 11: "focusable", 12: "focused", 20: "pressed", 22: "selectable", 23: "selected", 24: "sensitive", 25: "showing", 26: "indeterminate", 27: "stale", 28: "transient", 29: "vertical", 30: "visible", 33: "required", 34: "protected"}
	return states[state]
}

func relationName(relation uint32) string {
	relations := map[uint32]string{
		0: "label-for", 1: "labelled-by", 2: "controller-for", 3: "controlled-by",
		4: "member-of", 5: "tooltip-for", 6: "described-by", 7: "description-for",
		8: "node-child-of", 9: "flows-to", 10: "flows-from", 11: "subwindow-of",
		12: "embeds", 13: "embedded-by", 14: "popup-for", 15: "parent-window-of",
		16: "details", 17: "details-for", 18: "error-message", 19: "error-for",
	}
	return relations[relation]
}
