// Package accessibility exposes the read-only AT-SPI accessibility tree when
// an accessibility bus is available. It deliberately uses the same godbus
// dependency as the rest of perfuncted so callers do not need CGO or libatspi.
package accessibility

import (
	"context"
	"errors"
	"fmt"
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
	// ErrAmbiguous indicates that a selector matched more than one application.
	ErrAmbiguous = errors.New("accessibility: ambiguous application")
)

const (
	busService         = "org.a11y.Bus"
	busPath            = dbus.ObjectPath("/org/a11y/bus")
	registryName       = "org.a11y.atspi.Registry"
	registryPath       = dbus.ObjectPath("/org/a11y/atspi/registry")
	desktopPath        = dbus.ObjectPath("/org/a11y/atspi/accessible/root")
	nullObjectPath     = dbus.ObjectPath("/org/a11y/atspi/null")
	accessibleIface    = "org.a11y.atspi.Accessible"
	componentIface     = "org.a11y.atspi.Component"
	textIface          = "org.a11y.atspi.Text"
	propertiesIface    = "org.freedesktop.DBus.Properties"
	defaultMaxDepth    = 32
	defaultMaxNodes    = 10000
	defaultMaxText     = 4096
	defaultEventBuffer = 64
	maxApplications    = 1024
	cacheTTL           = 250 * time.Millisecond
	absMaxDepth        = 64
	absMaxNodes        = 100000
	absMaxText         = 1 << 20
)

// NodeID is an AT-SPI object reference. BusName and ObjectPath are opaque and
// should be retained together; object paths are not globally unique.
type NodeID struct {
	BusName    string `json:"busName"`
	ObjectPath string `json:"objectPath"`
}

func (id NodeID) valid() bool {
	return id.BusName != "" && id.ObjectPath != "" && id.ObjectPath != string(nullObjectPath)
}

type objectRef struct {
	BusName    string
	ObjectPath dbus.ObjectPath
}

func (r objectRef) id() NodeID {
	return NodeID{BusName: r.BusName, ObjectPath: string(r.ObjectPath)}
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
	Focused     bool              `json:"focused"`
	Visible     bool              `json:"visible"`
	Showing     bool              `json:"showing"`
	Enabled     bool              `json:"enabled"`
	// Redacted is true when sensitive/protected content was intentionally
	// removed. Callers can still use the node's role, bounds, and state.
	Redacted bool `json:"redacted,omitempty"`
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
	// AllowSensitive disables the default redaction of AT-SPI sensitive and
	// protected text/value attributes. Use only for an explicit trusted flow.
	AllowSensitive bool `json:"allowSensitive,omitempty"`
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
	Name string `json:"name,omitempty"`
	Role string `json:"role,omitempty"`
	Text string `json:"text,omitempty"`
}

// ApplicationFilter selects an application root without relying on a window
// title. Empty fields are ignored; PID is an exact process match.
type ApplicationFilter struct {
	Name string `json:"name,omitempty"`
	PID  int32  `json:"pid,omitempty"`
	Bus  string `json:"busName,omitempty"`
}

// Event is an invalidation-oriented AT-SPI signal. Signals are hints: callers
// should refresh a Snapshot before acting on a node because objects can be
// destroyed between notification and query.
type Event struct {
	Kind      string    `json:"kind"`
	Node      NodeID    `json:"node"`
	Property  string    `json:"property,omitempty"`
	Value     string    `json:"value,omitempty"`
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

// Backend is the read-only AT-SPI surface used by perfuncted's bundle.
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

// OpenRuntime opens the accessibility bus associated with rt. The normal
// session bus only provides org.a11y.Bus.GetAddress; all chatty AT-SPI calls
// are made over the returned private accessibility bus.
func OpenRuntime(rt env.Runtime) (Backend, error) {
	session, err := dbusutil.SessionBusAddress(rt.Get("DBUS_SESSION_BUS_ADDRESS"))
	if err != nil {
		return nil, fmt.Errorf("accessibility: connect to session bus: %w", err)
	}
	var address string
	call := session.Object(busService, busPath).Call("%s.GetAddress", 0)
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
	return &dbusBackend{session: session, access: access, generation: 1}, nil
}

type dbusBackend struct {
	session    *dbus.Conn
	access     *dbus.Conn
	mu         sync.RWMutex
	generation uint64
	cache      map[string]cachedSnapshot
}

type cachedSnapshot struct {
	at       time.Time
	snapshot Snapshot
}

func (b *dbusBackend) SupportedOperations() []string {
	return []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events"}
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

// Invalidate advances the generation and clears the bounded snapshot cache.
// The node is currently advisory; AT-SPI object paths may be reused, so a
// global generation is safer than retaining stale per-node data.
func (b *dbusBackend) Invalidate(_ NodeID) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.generation++
	b.cache = nil
	b.mu.Unlock()
}

func (b *dbusBackend) Close() error {
	var errs []error
	if b == nil {
		return nil
	}
	b.mu.Lock()
	access, session := b.access, b.session
	b.access, b.session = nil, nil
	b.mu.Unlock()
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
		return nil, errors.New("accessibility: bus is closed")
	}
	b.mu.RLock()
	access := b.access
	b.mu.RUnlock()
	if access == nil {
		return nil, errors.New("accessibility: bus is closed")
	}
	if !id.valid() {
		return nil, errors.New("accessibility: invalid object reference")
	}
	return access.Object(id.BusName, dbus.ObjectPath(id.ObjectPath)), nil
}

func (b *dbusBackend) desktop() NodeID {
	return NodeID{BusName: registryName, ObjectPath: string(desktopPath)}
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
		node, err := b.readNode(ctx, ref.id(), NodeID{}, defaultMaxText, false)
		if err != nil {
			continue
		}
		app := Application{Node: node}
		app.PID, _ = b.connectionPID(ctx, ref.BusName)
		_ = b.property(ctx, ref.id(), "org.a11y.atspi.Application", "ToolkitName", &app.ToolkitName)
		_ = b.property(ctx, ref.id(), "org.a11y.atspi.Application", "ToolkitVersion", &app.ToolkitVersion)
		apps = append(apps, app)
	}
	return apps, nil
}

// FindApplication returns exactly one application matching filter. Name is
// matched against the accessible name and PID/Bus are exact constraints.
func (b *dbusBackend) FindApplication(ctx context.Context, filter ApplicationFilter) (Application, error) {
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
		return nil, errors.New("accessibility: bus is closed")
	}
	b.mu.RLock()
	access := b.access
	b.mu.RUnlock()
	if access == nil {
		return nil, errors.New("accessibility: bus is closed")
	}
	opts = opts.normalized()
	registered := []string{"object:property-change", "object:state-changed", "window:activate", "window:deactivate"}
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
	matches := [][]dbus.MatchOption{
		{dbus.WithMatchInterface("org.a11y.atspi.Event.Object")},
		{dbus.WithMatchInterface("org.a11y.atspi.Event.Window")},
	}
	for _, match := range matches {
		if err := access.AddMatchSignalContext(ctx, match...); err != nil {
			for _, installed := range matches {
				_ = access.RemoveMatchSignal(installed...)
			}
			return nil, fmt.Errorf("accessibility: subscribe events: %w", err)
		}
	}
	signals := make(chan *dbus.Signal, opts.Buffer)
	access.Signal(signals)
	out := make(chan Event, opts.Buffer)
	go func() {
		defer close(out)
		defer access.RemoveSignal(signals)
		defer func() {
			for _, match := range matches {
				_ = access.RemoveMatchSignal(match...)
			}
			for _, eventType := range registered {
				_ = registry.CallWithContext(context.Background(), registryName+".DeregisterEvent", 0, eventType).Err
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				event := signalEvent(sig)
				b.Invalidate(event.Node)
				select {
				case out <- event:
				default:
				}
			}
		}
	}()
	return out, nil
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
	}
	return event
}

func (b *dbusBackend) Snapshot(ctx context.Context, root NodeID, opts SnapshotOptions) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, errors.New("accessibility: nil context")
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	opts = opts.normalized()
	if root == (NodeID{}) {
		root = b.desktop()
	} else if !root.valid() {
		return Snapshot{}, errors.New("accessibility: invalid snapshot root")
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
	return root.BusName + "\x00" + root.ObjectPath + "\x00" + strconv.Itoa(opts.MaxDepth) + "\x00" + strconv.Itoa(opts.MaxNodes) + "\x00" + strconv.Itoa(opts.MaxTextBytes) + "\x00" + strconv.FormatBool(opts.AllowSensitive)
}

func cloneSnapshot(in Snapshot) Snapshot {
	out := in
	out.Nodes = append([]Node(nil), in.Nodes...)
	out.Warnings = append([]string(nil), in.Warnings...)
	for i := range out.Nodes {
		out.Nodes[i].Interfaces = append([]string(nil), in.Nodes[i].Interfaces...)
		out.Nodes[i].States = append([]string(nil), in.Nodes[i].States...)
		out.Nodes[i].Children = append([]NodeID(nil), in.Nodes[i].Children...)
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
	backend  *dbusBackend
	opts     SnapshotOptions
	snapshot Snapshot
	seen     map[NodeID]struct{}
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
		child, childErr := w.walk(ctx, childRef.id(), id, depth+1)
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
		result = append(result, node)
	}
	return result, nil
}

func (b *dbusBackend) Focused(ctx context.Context, opts SnapshotOptions) (Node, error) {
	if ctx == nil {
		return Node{}, errors.New("accessibility: nil context")
	}
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
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
	return b.readNode(ctx, ref.id(), NodeID{}, defaultMaxText, false)
}

func (b *dbusBackend) children(ctx context.Context, id NodeID) ([]objectRef, error) {
	obj, err := b.object(id)
	if err != nil {
		return nil, err
	}
	var refs []objectRef
	if err := obj.CallWithContext(ctx, accessibleIface+".GetChildren", 0).Store(&refs); err != nil {
		return nil, fmt.Errorf("accessibility: children %s: %w", id.ObjectPath, err)
	}
	return refs, nil
}

func (b *dbusBackend) readNode(ctx context.Context, id, parent NodeID, maxText int, allowSensitive bool) (Node, error) {
	if !id.valid() {
		return Node{}, errors.New("accessibility: invalid node")
	}
	node := Node{ID: id, Parent: parent}
	_ = b.property(ctx, id, accessibleIface, "Name", &node.Name)
	_ = b.property(ctx, id, accessibleIface, "Description", &node.Description)
	b.readNodeRoleAndCount(ctx, id, &node)
	node.Interfaces = b.readNodeInterfaces(ctx, id)
	b.readNodeStates(ctx, id, &node)
	b.readNodeComponent(ctx, id, node.Interfaces, &node)
	b.readNodeText(ctx, id, node.Interfaces, maxText, &node)
	b.readNodeAttributes(ctx, id, &node)
	if !allowSensitive && hasSensitiveState(node.States) {
		redactSensitiveNode(&node)
	}
	return node, nil
}

func redactSensitiveNode(node *Node) {
	if node == nil {
		return
	}
	node.Redacted = true
	node.Text = ""
	for key := range node.Attributes {
		lower := strings.ToLower(key)
		if key == "value" || strings.Contains(lower, "text") || strings.Contains(lower, "password") {
			delete(node.Attributes, key)
		}
	}
}

func hasSensitiveState(states []string) bool {
	for _, state := range states {
		if state == "sensitive" || state == "protected" {
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
