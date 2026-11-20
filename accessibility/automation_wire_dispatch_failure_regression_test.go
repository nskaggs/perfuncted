package accessibility

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

type malformedActionReply struct {
	mu      sync.Mutex
	indices []int32
}

type cancelMutationReply struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	calls  int
}

func (p *cancelMutationReply) CopyText(_, _ int32) *dbus.Error {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	p.cancel()
	return nil
}

func (p *cancelMutationReply) GrabFocus() (bool, *dbus.Error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	p.cancel()
	return true, nil
}

func (p *malformedActionReply) DoAction(index int32) (string, *dbus.Error) {
	p.mu.Lock()
	p.indices = append(p.indices, index)
	p.mu.Unlock()
	return "not a boolean", nil
}

func TestMutationCancellationAfterProviderReceivesRequest(t *testing.T) {
	for _, method := range []string{"copy", "focus"} {
		t.Run(method, func(t *testing.T) {
			service, backend, _ := startSyntheticEventBus(t, nil)
			reply, err := service.RequestName("org.test.Application", dbus.NameFlagDoNotQueue)
			if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
				t.Fatalf("own test application name: %v %v", reply, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := &cancelMutationReply{cancel: cancel}
			path := dbus.ObjectPath("/synthetic/mutation")
			for _, iface := range []string{editableTextIface, componentIface} {
				if err := service.Export(provider, path, iface); err != nil {
					t.Fatal(err)
				}
			}
			id := NodeID{BusName: "org.test.Application", ObjectPath: string(path), Generation: backend.Generation(), Incarnation: 1}
			if method == "copy" {
				err = backend.CopyText(ctx, id, 0, 2)
			} else {
				err = backend.GrabFocus(ctx, id)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("provider-side cancellation acknowledged as success: %v", err)
			}
			provider.mu.Lock()
			calls := provider.calls
			provider.mu.Unlock()
			if calls != 1 {
				t.Fatalf("provider received %d mutations, want one", calls)
			}
		})
	}
}

func TestActionDispatchReportsUnknownForMalformedOrFailedWireReply(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "provider failure"
		if malformed {
			name = "malformed reply"
		}
		t.Run(name, func(t *testing.T) {
			service, backend, _ := startSyntheticEventBus(t, nil)
			reply, err := service.RequestName("org.test.Application", dbus.NameFlagDoNotQueue)
			if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
				t.Fatalf("own test application name: %v %v", reply, err)
			}
			path := dbus.ObjectPath("/synthetic/action")
			id := NodeID{BusName: "org.test.Application", ObjectPath: string(path), Generation: backend.Generation(), Incarnation: 1}
			var replacement *malformedActionReply
			var provider *actionWireFake
			if malformed {
				replacement = &malformedActionReply{}
				err = service.Export(replacement, path, actionIface)
			} else {
				provider = &actionWireFake{unknown: true}
				err = service.Export(provider, path, actionIface)
			}
			if err != nil {
				t.Fatal(err)
			}
			action := Action{Index: 1, Name: "open"}
			err = backend.dispatchAction(context.Background(), id, action)
			var invocation *ActionInvocationError
			if !errors.As(err, &invocation) || invocation.Dispatch != DispatchUnknown || invocation.Action != action {
				t.Fatalf("wire reply did not preserve dispatch uncertainty: %v", err)
			}
			if malformed {
				replacement.mu.Lock()
				indices := append([]int32(nil), replacement.indices...)
				replacement.mu.Unlock()
				if len(indices) != 1 || indices[0] != action.Index {
					t.Fatalf("unexpected action dispatch: %v", indices)
				}
			} else {
				provider.mu.Lock()
				indices := append([]int32(nil), provider.actions...)
				provider.mu.Unlock()
				if !strings.Contains(err.Error(), "remote action rejected") || len(indices) != 1 || indices[0] != action.Index {
					t.Fatalf("provider failure or dispatch lost: indices=%v err=%v", indices, err)
				}
			}
		})
	}
}
