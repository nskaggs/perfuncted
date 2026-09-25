package accessibility

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

type cachedSnapshot struct {
	at       time.Time
	snapshot Snapshot
}

// cacheObjectRef and cacheItem mirror the current Cache.GetItems wire shape:
// a((so)(so)(so)iiassusau). Keep these private so protocol details do not leak
// into the public API. A payload that cannot be decoded invalidates the local
// cache and leaves direct reads authoritative.
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

func (b *dbusBackend) children(ctx context.Context, id NodeID) ([]objectRef, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	if cached := b.cachedChildren(id); cached != nil {
		return cached, nil
	}
	expected := id.Generation
	obj, err := b.object(id)
	if err != nil {
		return nil, err
	}
	var refs []objectRef
	if err := obj.CallWithContext(ctx, accessibleIface+".GetChildren", 0).Store(&refs); err != nil {
		if isDisconnectedDBusError(err) {
			return nil, fmt.Errorf("accessibility: children %s: %w: %w", id.ObjectPath, ErrDisconnected, err)
		}
		return nil, fmt.Errorf("accessibility: children %s: %w", id.ObjectPath, err)
	}
	if err := b.generationError(expected); err != nil {
		return nil, err
	}
	return b.tagRefs(refs), nil
}

func isDisconnectedDBusError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrDisconnected) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "disconnected") || strings.Contains(msg, "message recipient") || strings.Contains(msg, "no reply")
}

func (b *dbusBackend) cachedChildren(id NodeID) []objectRef {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.cacheItems) == 0 || !b.cacheApps[id.BusName] || id.Generation != b.generation || b.closed || b.disconnected {
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

func (b *dbusBackend) loadCache(ctx context.Context, busName string) error { //nolint:gocyclo // cache loading has explicit protocol, generation, and publication guards.
	if ctx == nil {
		return errors.New("accessibility: nil context")
	}
	if b == nil || strings.TrimSpace(busName) == "" || busName == registryName {
		return ErrUnsupported
	}
	expected := b.Generation()
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
	if err := b.generationError(expected); err != nil {
		return err
	}
	b.mu.Lock()
	if b.generation != expected || b.closed || b.disconnected {
		current := b.generation
		b.mu.Unlock()
		return fmt.Errorf("%w: expected %d, current %d: %w", ErrStaleGeneration, expected, current, ErrStaleNode)
	}
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
		// An unrecognized cache signal cannot safely update the local item map.
		// Invalidate it and let the next snapshot reload authoritative state
		// rather than guessing at the payload shape.
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

func (b *dbusBackend) cachedItem(id NodeID) (cacheItem, bool) {
	if b == nil {
		return cacheItem{}, false
	}
	b.mu.RLock()
	if id.Generation != b.generation || b.closed || b.disconnected {
		b.mu.RUnlock()
		return cacheItem{}, false
	}
	item, ok := b.cacheItems[id]
	b.mu.RUnlock()
	return item, ok
}
