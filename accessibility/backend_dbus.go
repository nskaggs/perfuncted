package accessibility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/capability"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
)

const (
	busService         = "org.a11y.Bus"
	busAddressMethod   = busService + ".GetAddress"
	busPath            = dbus.ObjectPath("/org/a11y/bus")
	registryName       = "org.a11y.atspi.Registry"
	registryPath       = dbus.ObjectPath("/org/a11y/atspi/registry")
	desktopPath        = dbus.ObjectPath("/org/a11y/atspi/accessible/root")
	nullObjectPath     = dbus.ObjectPath("/org/a11y/atspi/null")
	accessibleIface    = "org.a11y.atspi.Accessible"
	componentIface     = "org.a11y.atspi.Component"
	textIface          = "org.a11y.atspi.Text"
	editableTextIface  = "org.a11y.atspi.EditableText"
	valueIface         = "org.a11y.atspi.Value"
	actionIface        = "org.a11y.atspi.Action"
	selectionIface     = "org.a11y.atspi.Selection"
	tableIface         = "org.a11y.atspi.Table"
	documentIface      = "org.a11y.atspi.Document"
	cacheIface         = "org.a11y.atspi.Cache"
	cachePath          = dbus.ObjectPath("/org/a11y/atspi/cache")
	propertiesIface    = "org.freedesktop.DBus.Properties"
	defaultMaxDepth    = 32
	defaultMaxNodes    = 10000
	defaultMaxText     = 4096
	defaultMaxTotal    = 1 << 20
	defaultEventBuffer = 64
	maxApplications    = 1024
	// Cache and event coalescing windows govern backend bookkeeping, not
	// caller-visible operation deadlines.
	cacheTTL            = 250 * time.Millisecond
	eventCoalesceWindow = 10 * time.Millisecond
	// Event setup/cleanup are lifecycle guards; the returned stream remains
	// caller-owned and does not inherit an artificial expiry.
	eventSetupTimeout    = 2 * time.Second
	eventCleanupTimeout  = 750 * time.Millisecond
	eventStartRetryLimit = 1
	absMaxDepth          = 64
	absMaxNodes          = 100000
	absMaxText           = 1 << 20
	absMaxTotal          = 16 << 20
)

// objectRef is an AT-SPI wire reference. NodeID adds the local generation
// required to keep handles scoped to one live backend lifecycle.
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

func OpenRuntime(rt env.Runtime) (Backend, error) {
	return OpenRuntimeContext(context.Background(), rt)
}

// OpenRuntimeContext is the cancellable form of OpenRuntime. Connections and
// the accessibility-bus address lookup are owned by the returned backend once
// this function succeeds.
func OpenRuntimeContext(ctx context.Context, rt env.Runtime) (Backend, error) {
	return openRuntime(ctx, rt, 1)
}

func openRuntime(ctx context.Context, rt env.Runtime, generation uint64) (Backend, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	session, err := dbusutil.SessionBusAddressContext(ctx, busAddress)
	if err != nil {
		return nil, fmt.Errorf("accessibility: connect to session bus: %w", err)
	}
	// ATSPI_BUS_ADDRESS is the explicit accessibility-bus address override.
	// AT_SPI_BUS is an X root-window property and is intentionally ignored.
	address := strings.TrimSpace(rt.Get("ATSPI_BUS_ADDRESS"))
	if address == "" {
		call := session.Object(busService, busPath).CallWithContext(ctx, busAddressMethod, 0)
		if storeErr := call.Store(&address); storeErr != nil {
			_ = session.Close()
			return nil, fmt.Errorf("accessibility: get accessibility bus address: %w", storeErr)
		}
		address = strings.TrimSpace(address)
		if address == "" {
			_ = session.Close()
			return nil, fmt.Errorf("accessibility: empty bus address")
		}
	}
	access, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("accessibility: dial accessibility bus: %w", err)
	}
	backend := &dbusBackend{
		runtime: rt, session: session, access: access, generation: generation,
		cache:      make(map[string]cachedSnapshot),
		cacheItems: make(map[NodeID]cacheItem), cacheApps: make(map[string]bool),
		toolkits:    make(map[string]string),
		subscribers: make(map[uint64]*eventSubscriber),
	}
	backend.watchDisconnect()
	return backend, nil
}

// dbusBackend is the single owner of a target accessibility session: its two
// private bus connections, generation, snapshot/cache state, and event stream
// all share this lifecycle and are released by Close.
type dbusBackend struct {
	runtime      env.Runtime
	session      *dbus.Conn
	access       *dbus.Conn
	mu           sync.RWMutex
	toolkitMu    sync.RWMutex
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
	toolkits       map[string]string
	eventsMu       sync.Mutex
	subscribers    map[uint64]*eventSubscriber
	nextSubscriber uint64
	eventCancel    context.CancelFunc
	eventDone      chan struct{}
	eventAccess    *dbus.Conn
	eventStarting  *eventStart
	eventStartStop context.CancelFunc
	// callOverride is used only by deterministic package tests to exercise
	// protocol and error handling without a host D-Bus daemon.
	callOverride func(context.Context, NodeID, string, []any) (any, error)
}

func (b *dbusBackend) SupportedOperations() []string {
	return capability.Operations("accessibility")
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
	b.toolkitMu.Lock()
	b.toolkits = nil
	b.toolkitMu.Unlock()
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
	b.toolkitMu.Lock()
	b.toolkits = nil
	b.toolkitMu.Unlock()
	b.stopEvents(access)
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

// toolkitForNode resolves and caches the toolkit that owns a node's
// application. Toolkit identity is scoped to the current backend generation;
// invalidation clears the cache so an object reference is never reused across
// sessions.
func (b *dbusBackend) toolkitForNode(ctx context.Context, id NodeID) (string, error) {
	if err := mutationContext(ctx, "GetApplication"); err != nil {
		return "", err
	}
	if err := b.validateHandle(id); err != nil {
		return "", err
	}
	b.toolkitMu.RLock()
	toolkit, ok := b.toolkits[id.BusName]
	b.toolkitMu.RUnlock()
	if ok {
		return toolkit, nil
	}
	obj, err := b.object(id)
	if err != nil {
		return "", err
	}
	var application objectRef
	if err := obj.CallWithContext(ctx, accessibleIface+".GetApplication", 0).Store(&application); err != nil {
		return "", fmt.Errorf("accessibility: get application: %w", err)
	}
	if application.null() {
		return "", fmt.Errorf("accessibility: node has no application")
	}
	var name string
	if err := b.property(ctx, b.refID(application), "org.a11y.atspi.Application", "ToolkitName", &name); err != nil {
		return "", err
	}
	if err := b.generationError(id.Generation); err != nil {
		return "", err
	}
	b.toolkitMu.Lock()
	if b.toolkits == nil {
		b.toolkits = make(map[string]string)
	}
	b.toolkits[id.BusName] = name
	b.toolkitMu.Unlock()
	return name, nil
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
	for attempt := 0; attempt < 2; attempt++ {
		expected := b.Generation()
		refs, err := b.children(ctx, b.desktop())
		if err != nil {
			if errors.Is(err, ErrStaleGeneration) {
				continue
			}
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
			if err := b.loadCache(ctx, ref.BusName); err != nil && errors.Is(err, ErrStaleGeneration) {
				apps = nil
				break
			}
			id := b.refID(ref)
			node, err := b.readNode(ctx, id, NodeID{}, defaultMaxText, false)
			if errors.Is(err, ErrStaleGeneration) {
				apps = nil
				break
			}
			if err != nil {
				continue
			}
			app := Application{Node: node}
			app.PID, _ = b.connectionPID(ctx, ref.BusName)
			_ = b.property(ctx, id, "org.a11y.atspi.Application", "ToolkitName", &app.ToolkitName)
			_ = b.property(ctx, id, "org.a11y.atspi.Application", "ToolkitVersion", &app.ToolkitVersion)
			apps = append(apps, app)
		}
		if apps == nil {
			continue
		}
		if err := b.generationError(expected); err != nil {
			continue
		}
		return apps, nil
	}
	return nil, fmt.Errorf("%w: bounded retry exhausted", ErrStaleGeneration)
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
