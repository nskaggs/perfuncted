package accessibility

import (
	"context"
	"errors"
	"testing"
)

func TestATSPIAlreadyLoadedCacheRejectsCanceledCaller(t *testing.T) {
	b := &dbusBackend{generation: 7, cacheItems: map[NodeID]cacheItem{}, cacheApps: map[string]bool{"org.example.Private": true}}
	if err := b.loadCache(context.Background(), "org.example.Private"); err != nil {
		t.Fatalf("valid loaded cache refused: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.loadCache(ctx, "org.example.Private"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cache lookup reported success: %v", err)
	}
}
