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

// objectRef is an AT-SPI wire reference. NodeID adds the backend epoch and
// object incarnation required to keep handles scoped to live provider objects.
type objectRef struct {
	BusName    string
	ObjectPath dbus.ObjectPath
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
		incarnations: make(map[objectIdentity]uint64), parents: make(map[objectIdentity]objectIdentity),
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
	runtime    env.Runtime
	session    *dbus.Conn
	access     *dbus.Conn
	mu         sync.RWMutex
	generation uint64
	// observationRevision tracks provider-state consistency. cacheRevision also
	// tracks cache-only updates that do not change a direct AT-SPI observation.
	observationRevision uint64
	cacheRevision       uint64
	observationHistory  []observationChange
	observationFloor    uint64
	disconnected        bool
	closed              bool
	cache               map[string]cachedSnapshot
	// cacheItems is the optional upstream AT-SPI cache. It is keyed by the
	// complete object reference, so objects from different application buses
	// cannot collide. The local snapshot cache remains a short-lived response
	// optimization; cacheItems is refreshed by Cache.GetItems and signals.
	cacheItems map[NodeID]cacheItem
	// cacheChildrenIndex is a lazy, sorted child lookup derived from cacheItems,
	// and cacheChildrenIndexGen is the cache generation it was built from. The
	// index is keyed only by bus name and object path, so without the generation a
	// reload that produced a new generation would keep serving the previous
	// generation's children.
	cacheChildrenIndex    map[objectIdentity][]objectRef
	cacheChildrenIndexGen uint64
	cacheApps             map[string]bool
	// incarnations prevent a reused object path from reviving IDs issued for a
	// removed object. parents records the latest observed ancestry for subtree
	// revocation; cacheItems supplies ancestry only when no direct edge is known.
	incarnations             map[objectIdentity]uint64
	parents                  map[objectIdentity]objectIdentity
	parentTrackingIncomplete bool
	toolkits                 map[string]string
	eventsMu                 sync.Mutex
	subscribers              map[uint64]*eventSubscriber
	nextSubscriber           uint64
	eventCancel              context.CancelFunc
	eventDone                chan struct{}
	eventAccess              *dbus.Conn
	eventRegistered          []string
	eventStarting            *eventStart
	eventStartStop           context.CancelFunc
	eventsRetired            bool
	// callOverride is used only by deterministic package tests to exercise
	// protocol and error handling without a host D-Bus daemon.
	callOverride func(context.Context, NodeID, string, []any) (any, error)
}

func (b *dbusBackend) SupportedOperations() []string {
	return capability.Operations("accessibility")
}

// Generation returns the backend-wide handle epoch. Object-specific removal
// and path reuse are guarded separately by NodeID.Incarnation.
func (b *dbusBackend) Generation() uint64 {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.generation
}

// Invalidate advances the object-handle epoch and clears all observations.
func (b *dbusBackend) Invalidate(_ NodeID) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.generation++
	b.observationRevision++
	b.cacheRevision++
	b.cache, b.cacheItems, b.cacheApps, b.toolkits = nil, nil, nil, nil
	b.cacheChildrenIndex = nil
	b.cacheChildrenIndexGen = 0
	b.incarnations, b.parents = nil, nil
	b.parentTrackingIncomplete = false
	b.mu.Unlock()
}

func (b *dbusBackend) incarnationLocked(id objectIdentity) uint64 {
	if b.incarnations == nil {
		b.incarnations = make(map[objectIdentity]uint64)
	}
	if b.incarnations[id] == 0 {
		if len(b.incarnations) >= maxTrackedObjects {
			b.parentTrackingIncomplete = true
			return 1
		}
		b.incarnations[id] = 1
	}
	return b.incarnations[id]
}

func (b *dbusBackend) nodeIDLocked(ref objectRef) NodeID {
	identity := objectIdentity{busName: ref.BusName, objectPath: string(ref.ObjectPath)}
	return NodeID{BusName: ref.BusName, ObjectPath: string(ref.ObjectPath), Generation: b.generation, Incarnation: b.incarnationLocked(identity)}
}

func (b *dbusBackend) recordObjectParent(child, parent NodeID) {
	if b == nil || !child.valid() || !parent.valid() {
		return
	}
	b.mu.Lock()
	childIdentity := objectIdentity{busName: child.BusName, objectPath: child.ObjectPath}
	parentIdentity := objectIdentity{busName: parent.BusName, objectPath: parent.ObjectPath}
	if b.handleCurrentLocked(child) && b.handleCurrentLocked(parent) && b.incarnationLocked(childIdentity) == effectiveIncarnation(child) && b.incarnationLocked(parentIdentity) == effectiveIncarnation(parent) {
		b.recordParentLocked(cacheObjectRef{BusName: child.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)}, cacheObjectRef{BusName: parent.BusName, ObjectPath: dbus.ObjectPath(parent.ObjectPath)})
	}
	b.mu.Unlock()
}

func (b *dbusBackend) recordParentLocked(child, parent cacheObjectRef) {
	childIdentity := objectIdentity{busName: child.BusName, objectPath: string(child.ObjectPath)}
	parentIdentity := objectIdentity{busName: parent.BusName, objectPath: string(parent.ObjectPath)}
	if !childIdentity.valid() || !parentIdentity.valid() || b.closed || b.disconnected {
		return
	}
	if b.incarnations == nil {
		b.incarnations = make(map[objectIdentity]uint64)
	}
	if b.parents == nil {
		b.parents = make(map[objectIdentity]objectIdentity)
	}
	if _, exists := b.parents[childIdentity]; !exists && len(b.parents) >= maxTrackedObjects {
		b.parentTrackingIncomplete = true
		return
	}
	b.parents[childIdentity] = parentIdentity
}

func effectiveIncarnation(id NodeID) uint64 {
	if id.Incarnation == 0 {
		return 1
	}
	return id.Incarnation
}

func (b *dbusBackend) handleCurrentLocked(id NodeID) bool {
	current := b.incarnations[objectIdentity{busName: id.BusName, objectPath: id.ObjectPath}]
	if current == 0 {
		current = 1
	}
	return id.Generation == b.generation && effectiveIncarnation(id) == current && !b.closed && !b.disconnected
}

const maxTrackedObjects = 100000

// revokeObjectLocked invalidates a removed object and every descendant whose
// latest observed ancestry is known. It returns false when ancestry or
// incarnation tracking is incomplete, requiring a backend-wide epoch transition.
func (b *dbusBackend) revokeObjectLocked(root objectIdentity) bool {
	if !root.valid() || b.parentTrackingIncomplete {
		return false
	}
	if b.incarnations == nil {
		b.incarnations = make(map[objectIdentity]uint64)
	}
	removed := b.removedObjectTreeLocked(root)
	if !b.canTrackRevokedObjectsLocked(removed) {
		return false
	}
	b.detachForeignParentEdgesLocked(removed)
	b.adjustCachedChildrenForRevocationLocked(removed)
	for identity := range removed {
		current := b.incarnationLocked(identity)
		b.incarnations[identity] = current + 1
		delete(b.parents, identity)
	}
	for id := range b.cacheItems {
		identity := objectIdentity{busName: id.BusName, objectPath: id.ObjectPath}
		if _, ok := removed[identity]; ok {
			delete(b.cacheItems, id)
		}
	}
	b.cacheChildrenIndex = nil
	b.cacheChildrenIndexGen = 0
	b.cache = nil
	return true
}

func (b *dbusBackend) removedObjectTreeLocked(root objectIdentity) map[objectIdentity]struct{} {
	children := make(map[objectIdentity][]objectIdentity, len(b.parents))
	for child, parent := range b.parents {
		if child.busName != parent.busName {
			continue
		}
		children[parent] = append(children[parent], child)
	}
	for _, item := range b.cacheItems {
		child := objectIdentity{busName: item.Object.BusName, objectPath: string(item.Object.ObjectPath)}
		if _, observed := b.parents[child]; observed {
			continue
		}
		parent := cacheParentIdentity(item.Parent)
		if child.busName != parent.busName {
			continue
		}
		children[parent] = append(children[parent], child)
	}
	removed := map[objectIdentity]struct{}{root: {}}
	queue := []objectIdentity{root}
	for i := 0; i < len(queue); i++ {
		for _, child := range children[queue[i]] {
			if _, seen := removed[child]; !seen {
				removed[child] = struct{}{}
				queue = append(queue, child)
			}
		}
	}
	return removed
}

func (b *dbusBackend) detachForeignParentEdgesLocked(removed map[objectIdentity]struct{}) {
	affectedApplications := make(map[string]struct{})
	for child, parent := range b.parents {
		if child.busName == parent.busName {
			continue
		}
		if _, parentRemoved := removed[parent]; parentRemoved {
			delete(b.parents, child)
			affectedApplications[child.busName] = struct{}{}
		}
	}
	for id, item := range b.cacheItems {
		child := objectIdentity{busName: id.BusName, objectPath: id.ObjectPath}
		parent := cacheParentIdentity(item.Parent)
		if child.busName != parent.busName {
			if _, parentRemoved := removed[parent]; parentRemoved {
				affectedApplications[child.busName] = struct{}{}
			}
		}
	}
	for busName := range affectedApplications {
		// A foreign child remains a live handle, but its owning cache can no
		// longer prove that it belongs beneath the removed parent identity.
		b.invalidateCacheApplicationLocked(busName)
	}
}

func (b *dbusBackend) canTrackRevokedObjectsLocked(removed map[objectIdentity]struct{}) bool {
	newIncarnations := 0
	for identity := range removed {
		if b.incarnations[identity] == 0 {
			newIncarnations++
		}
	}
	return len(b.incarnations)+newIncarnations <= maxTrackedObjects
}

func (b *dbusBackend) adjustCachedChildrenForRevocationLocked(removed map[objectIdentity]struct{}) {
	parentsByChild := make(map[objectIdentity]objectIdentity, len(b.parents)+len(b.cacheItems))
	for child, parent := range b.parents {
		parentsByChild[child] = parent
	}
	for id, item := range b.cacheItems {
		child := objectIdentity{busName: id.BusName, objectPath: id.ObjectPath}
		parent := objectIdentity{busName: item.Parent.BusName, objectPath: string(item.Parent.ObjectPath)}
		if observed, ok := parentsByChild[child]; ok {
			if observed != parent {
				b.invalidateCacheApplicationLocked(child.busName)
			}
			continue
		}
		parentsByChild[child] = parent
	}
	removedChildrenByParent := make(map[objectIdentity]int)
	for child, parent := range parentsByChild {
		if _, childRemoved := removed[child]; !childRemoved {
			continue
		}
		if _, parentRemoved := removed[parent]; !parentRemoved {
			removedChildrenByParent[parent]++
		}
	}
	for parent, count := range removedChildrenByParent {
		b.adjustCachedChildCountLocked(
			cacheObjectRef{BusName: parent.busName, ObjectPath: dbus.ObjectPath(parent.objectPath)},
			-int64(count),
		)
	}
}

func (b *dbusBackend) invalidateCacheApplicationLocked(busName string) {
	b.cacheChildrenIndex = nil
	b.cacheChildrenIndexGen = 0
	if b.cacheApps != nil {
		b.cacheApps[busName] = false
	}
	for id := range b.cacheItems {
		if id.BusName == busName {
			delete(b.cacheItems, id)
		}
	}
	b.cache = nil
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
	b.observationRevision++
	b.cacheRevision++
	access, session := b.access, b.session
	b.access, b.session = nil, nil
	b.cache, b.cacheItems, b.cacheApps = nil, nil, nil
	b.cacheChildrenIndex = nil
	b.cacheChildrenIndexGen = 0
	b.toolkits = nil
	b.mu.Unlock()
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
	toolkit, ok, err := b.cachedToolkit(id)
	if err != nil {
		return "", err
	}
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
	if err := b.cacheToolkit(id, name); err != nil {
		return "", err
	}
	return name, nil
}

func (b *dbusBackend) cachedToolkit(id NodeID) (string, bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.handleCurrentLocked(id) {
		return "", false, ErrStaleNode
	}
	toolkit, ok := b.toolkits[id.BusName]
	return toolkit, ok, nil
}

func (b *dbusBackend) cacheToolkit(id NodeID, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.handleCurrentLocked(id) {
		return ErrStaleNode
	}
	if b.toolkits == nil {
		b.toolkits = make(map[string]string)
	}
	b.toolkits[id.BusName] = name
	return nil
}

func (b *dbusBackend) validateHandle(id NodeID) error {
	if b == nil {
		return ErrDisconnected
	}
	b.mu.RLock()
	current := b.handleCurrentLocked(id)
	disconnected, closed := b.disconnected, b.closed
	b.mu.RUnlock()
	if disconnected || closed {
		return ErrDisconnected
	}
	if !id.valid() {
		return ErrStaleNode
	}
	if !current {
		return ErrStaleNode
	}
	return nil
}

func (b *dbusBackend) desktop() NodeID {
	return b.refID(objectRef{BusName: registryName, ObjectPath: desktopPath})
}

func (b *dbusBackend) refID(ref objectRef) NodeID {
	if b == nil {
		return NodeID{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nodeIDLocked(ref)
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

// FindApplicationFresh resolves an application from current desktop children
// and identity properties. It does not load or consult the AT-SPI cache.
func (b *dbusBackend) FindApplicationFresh(ctx context.Context, filter ApplicationFilter) (Application, error) { //nolint:gocyclo // fresh lookup keeps identity, completeness, and generation checks in one bounded resolution.
	if ctx == nil {
		return Application{}, errors.New("accessibility: nil context")
	}
	want := strings.ToLower(strings.TrimSpace(filter.Name))
	wantTitle := strings.ToLower(strings.TrimSpace(filter.WindowTitle))
	var lastObservationErr error
	for attempt := 0; attempt < 2; attempt++ {
		expected := b.Generation()
		b.mu.RLock()
		expectedObservation := b.observationRevision
		b.mu.RUnlock()
		desktop := b.desktop()
		refs, err := b.childrenFresh(ctx, desktop)
		if err != nil {
			if errors.Is(err, ErrStaleGeneration) {
				continue
			}
			return Application{}, err
		}
		if len(refs) > maxApplications {
			return Application{}, fmt.Errorf("%w: application list exceeds %d entries", ErrIncompleteSnapshot, maxApplications)
		}
		matches := make([]Application, 0, 1)
		observedApps := make([]Node, 0, len(refs))
		for _, ref := range refs {
			if err := ctx.Err(); err != nil {
				return Application{}, err
			}
			if filter.Bus != "" && ref.BusName != filter.Bus {
				continue
			}
			id := b.refID(ref)
			if filter.WindowID != "" && id.ObjectPath != filter.WindowID {
				continue
			}
			pid, pidErr := b.connectionPID(ctx, ref.BusName)
			if filter.PID != 0 {
				if pidErr != nil {
					return Application{}, fmt.Errorf("accessibility: resolve application PID: %w", pidErr)
				}
				if pid != filter.PID {
					continue
				}
			}
			var name string
			if err := b.property(ctx, id, accessibleIface, "Name", &name); err != nil {
				return Application{}, fmt.Errorf("accessibility: read application name: %w", err)
			}
			var description string
			if wantTitle != "" {
				if err := b.property(ctx, id, accessibleIface, "Description", &description); err != nil {
					return Application{}, fmt.Errorf("accessibility: read application description: %w", err)
				}
			}
			observedApps = append(observedApps, Node{ID: id, Name: name, Description: description})
			if want != "" && !strings.Contains(strings.ToLower(name), want) {
				continue
			}
			if wantTitle != "" && !strings.Contains(strings.ToLower(name), wantTitle) && !strings.Contains(strings.ToLower(description), wantTitle) {
				continue
			}
			matches = append(matches, Application{Node: Node{ID: id, Name: name, Description: description}, PID: pid})
		}
		if err := b.generationError(expected); err != nil {
			continue
		}
		if err := b.applicationResolutionChangesError(expectedObservation, desktop, observedApps); err != nil {
			lastObservationErr = err
			continue
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
	if lastObservationErr != nil {
		return Application{}, lastObservationErr
	}
	return Application{}, fmt.Errorf("%w: bounded fresh application resolution exhausted", ErrStaleGeneration)
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
