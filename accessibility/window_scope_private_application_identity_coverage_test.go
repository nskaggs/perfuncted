package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A window target identified by application identity alone requires actual
// provider-reported app name/description before narrowing candidates. Neither
// fresh nor cached correlation may assume an application name from its bus ID.
func TestPrivateATSPIFreshApplicationIdentityReadsProviderMetadata(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	ctx := context.Background()
	target := WindowTarget{ID: "managed-editor", AppID: "editor"}
	fresh, err := backend.windowApplicationsFresh(ctx, target)
	if err != nil || len(fresh) != 1 || !strings.EqualFold(fresh[0].Name, "Editor") {
		t.Fatalf("fresh application identity incomplete: apps=%+v err=%v", fresh, err)
	}
	cached, err := backend.windowApplications(ctx, target)
	if err != nil || len(cached) != 1 || !strings.EqualFold(cached[0].Name, "Editor") {
		t.Fatalf("cached application identity incomplete: apps=%+v err=%v", cached, err)
	}
	if !windowAppMatches(fresh[0], target) {
		t.Fatal("matching provider name rejected for app-only correlation")
	}
	if windowAppMatches(fresh[0], WindowTarget{ID: target.ID, AppID: "not-this-application"}) {
		t.Fatal("unrelated AppID matched only by provider bus address")
	}
}

// A retired or disconnected accessibility provider cannot supply an inferred
// window identity when the node has no cached properties.
func TestWindowCandidateIdentityReadRejectsDisconnectedProvider(t *testing.T) {
	backend := &dbusBackend{generation: 12}
	id := NodeID{BusName: "org.test.Private", ObjectPath: "/window", Generation: 12}
	parent := NodeID{BusName: id.BusName, ObjectPath: "/app", Generation: 12}
	node, err := backend.readWindowCandidate(context.Background(), id, parent)
	if !errors.Is(err, ErrDisconnected) || node.ID != (NodeID{}) || node.Name != "" {
		t.Fatalf("disconnected provider returned candidate %+v err=%v", node, err)
	}
	fresh, err := backend.readWindowCandidateFresh(context.Background(), id, parent, true)
	if !errors.Is(err, ErrDisconnected) || fresh.ID != (NodeID{}) || fresh.Name != "" {
		t.Fatalf("disconnected provider returned fresh candidate %+v err=%v", fresh, err)
	}
}
