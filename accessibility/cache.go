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
	return NodeID{BusName: item.Object.BusName, ObjectPath: string(item.Object.ObjectPath), Generation: generation, Incarnation: 1}
}

func (b *dbusBackend) children(ctx context.Context, id NodeID) ([]objectRef, error) {
	return b.childrenWithCache(ctx, id, true)
}

func (b *dbusBackend) childrenFresh(ctx context.Context, id NodeID) ([]objectRef, error) {
	return b.childrenWithCache(ctx, id, false)
}

func (b *dbusBackend) childrenWithCache(ctx context.Context, id NodeID, allowCache bool) ([]objectRef, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	if allowCache {
		if cached := b.cachedChildren(id); cached != nil {
			return cached, nil
		}
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
	if err := b.validateHandle(id); err != nil {
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
	if len(b.cacheItems) == 0 || !b.cacheApps[id.BusName] || !b.handleCurrentLocked(id) {
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
	expectedCacheRevision := b.cacheRevision
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
	return b.publishLoadedCache(busName, items, expected, expectedCacheRevision)
}

func (b *dbusBackend) publishLoadedCache(busName string, items []cacheItem, expectedGeneration, expectedCacheRevision uint64) error {
	if err := validateLoadedCacheItems(busName, items); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.generation != expectedGeneration || b.closed || b.disconnected {
		return fmt.Errorf("%w: expected %d, current %d: %w", ErrStaleGeneration, expectedGeneration, b.generation, ErrStaleNode)
	}
	if b.cacheRevision != expectedCacheRevision {
		// A signal updated or invalidated this cache while GetItems was in
		// flight. Keep the signal-owned state; the caller can continue with
		// direct AT-SPI reads.
		return nil
	}
	b.replaceCompleteCacheLocked(busName, items)
	return nil
}

func validateLoadedCacheItems(busName string, items []cacheItem) error {
	for _, item := range items {
		if !validCacheObjectRef(item.Object) || !validCacheObjectRef(item.Application) || !validCacheObjectRef(item.Parent) ||
			item.Object.BusName != busName || item.Application.BusName != busName {
			return fmt.Errorf("accessibility: cache get items for %s returned foreign or malformed identity", busName)
		}
	}
	return nil
}

func (b *dbusBackend) replaceCompleteCacheLocked(busName string, items []cacheItem) {
	if b.cacheItems == nil {
		b.cacheItems = make(map[NodeID]cacheItem)
	}
	for id := range b.cacheItems {
		if id.BusName == busName {
			delete(b.cacheItems, id)
		}
	}
	for child := range b.parents {
		if child.busName == busName {
			delete(b.parents, child)
		}
	}
	for _, item := range items {
		id := b.nodeIDLocked(objectRef{BusName: item.Object.BusName, ObjectPath: item.Object.ObjectPath})
		if id.valid() {
			b.cacheItems[id] = item
			b.recordParentLocked(item.Object, item.Parent)
		}
	}
	if b.cacheApps == nil {
		b.cacheApps = make(map[string]bool)
	}
	b.cacheApps[busName] = true
}

func (b *dbusBackend) applyCacheSignal(sig *dbus.Signal) {
	if b == nil || sig == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.applyCacheSignalLocked(sig)
}

func (b *dbusBackend) applyCacheSignalLocked(sig *dbus.Signal) {
	if b.cacheItems == nil {
		b.cacheItems = make(map[NodeID]cacheItem)
	}
	switch {
	case strings.HasSuffix(sig.Name, "Cache:AddAccessible"):
		item, ok := cacheItemFromSignal(sig.Body)
		if !ok || item.Object.BusName != sig.Sender {
			b.cacheItems, b.cacheApps, b.cache = nil, nil, nil
			return
		}
		b.applyCacheAddLocked(sig.Body)
	case strings.HasSuffix(sig.Name, "Cache:RemoveAccessible"):
		ref, ok := cacheObjectRefFromSignal(sig.Body)
		if !ok || ref.BusName != sig.Sender {
			b.cacheItems, b.cacheApps, b.cache = nil, nil, nil
			return
		}
		b.applyCacheRemoveLocked(sig.Body)
	}
	b.cache = nil
}

func (b *dbusBackend) applyCacheAddLocked(body []any) {
	item, ok := cacheItemFromSignal(body)
	if !ok {
		// An unrecognized cache signal cannot safely update the local item map.
		// Invalidate it and let the next snapshot reload authoritative state
		// rather than guessing at the payload shape.
		b.cacheItems, b.cacheApps = nil, nil
		return
	}
	if item.Object.BusName != item.Application.BusName || !validCacheObjectRef(item.Object) || !validCacheObjectRef(item.Application) || !validCacheObjectRef(item.Parent) {
		b.cacheItems, b.cacheApps = nil, nil
		return
	}
	id := b.nodeIDLocked(objectRef{BusName: item.Object.BusName, ObjectPath: item.Object.ObjectPath})
	if b.cacheApps[item.Object.BusName] {
		old, exists := b.cacheItems[id]
		if exists && old.Parent != item.Parent {
			b.adjustCachedChildCountLocked(old.Parent, -1)
		}
		if !exists || old.Parent != item.Parent {
			b.adjustCachedChildCountLocked(item.Parent, 1)
		}
	}
	b.cacheItems[id] = item
	b.recordParentLocked(item.Object, item.Parent)
}

func (b *dbusBackend) applyCacheRemoveLocked(body []any) {
	ref, ok := cacheObjectRefFromSignal(body)
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

func (b *dbusBackend) adjustCachedChildCountLocked(parent cacheObjectRef, delta int64) {
	if !parent.ObjectPath.IsValid() || !b.cacheApps[parent.BusName] {
		return
	}
	parentID := b.nodeIDLocked(objectRef(parent))
	item, ok := b.cacheItems[parentID]
	if !ok || item.ChildCount < 0 {
		b.cacheApps[parent.BusName] = false
		delete(b.cacheItems, parentID)
		return
	}
	updated := int64(item.ChildCount) + delta
	if updated < 0 || updated > 1<<31-1 {
		b.cacheApps[parent.BusName] = false
		delete(b.cacheItems, parentID)
		return
	}
	item.ChildCount = int32(updated)
	b.cacheItems[parentID] = item
}

func (b *dbusBackend) cachedItem(id NodeID) (cacheItem, bool) {
	if b == nil {
		return cacheItem{}, false
	}
	b.mu.RLock()
	if !b.handleCurrentLocked(id) {
		b.mu.RUnlock()
		return cacheItem{}, false
	}
	key := id
	if key.Incarnation == 0 {
		key.Incarnation = 1
	}
	item, ok := b.cacheItems[key]
	if !ok && id.Incarnation == 0 {
		item, ok = b.cacheItems[id]
	}
	b.mu.RUnlock()
	return item, ok
}
