package accessibility

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

type actionWireFake struct {
	mu             sync.Mutex
	accept         bool
	unknown        bool
	metadataDenied bool
	nameDenied     bool
	actions        []int32
	focusCalls     int
	copied         bool
	values         []float64
}

func (f *actionWireFake) GetActions() ([]actionMetadataWire, *dbus.Error) {
	f.mu.Lock()
	denied := f.metadataDenied
	f.mu.Unlock()
	if denied {
		return nil, dbus.MakeFailedError(errors.New("metadata denied"))
	}
	return []actionMetadataWire{{LocalizedName: "Localized Save", Description: "Save document", KeyBinding: "Ctrl+S"}, {LocalizedName: "Localized Open", Description: "Open", KeyBinding: "Ctrl+O"}}, nil
}
func (f *actionWireFake) GetName(index int32) (string, *dbus.Error) {
	f.mu.Lock()
	denied := f.nameDenied
	f.mu.Unlock()
	if denied {
		return "", dbus.MakeFailedError(errors.New("name denied"))
	}
	if index == 0 {
		return "save", nil
	}
	if index == 1 {
		return "open", nil
	}
	return "", dbus.MakeFailedError(errors.New("unknown action"))
}
func (f *actionWireFake) DoAction(index int32) (bool, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, index)
	if f.unknown {
		return false, dbus.MakeFailedError(errors.New("remote action rejected"))
	}
	return f.accept, nil
}
func (f *actionWireFake) GrabFocus() (bool, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focusCalls++
	if f.unknown {
		return false, dbus.MakeFailedError(errors.New("remote component rejected"))
	}
	return f.accept, nil
}
func (f *actionWireFake) CopyText(start, end int32) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if start == 0 && end == 2 {
		f.copied = true
	}
	if f.unknown {
		return dbus.MakeFailedError(errors.New("remote clipboard denied"))
	}
	return nil
}
func (f *actionWireFake) Set(iface, name string, value dbus.Variant) *dbus.Error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if iface != valueIface || name != "CurrentValue" {
		return dbus.MakeFailedError(errors.New("wrong property"))
	}
	v, ok := value.Value().(float64)
	if !ok {
		return dbus.MakeFailedError(errors.New("wrong value"))
	}
	f.values = append(f.values, v)
	if f.unknown {
		return dbus.MakeFailedError(errors.New("remote property refused"))
	}
	return nil
}

func newPrivateATSPIActionWireFixture(t *testing.T) (*dbusBackend, *actionWireFake, NodeID) {
	t.Helper()

	service, b, _ := startSyntheticEventBus(t, nil)
	reply, err := service.RequestName("org.test.Application", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("synthetic application name: reply=%v err=%v", reply, err)
	}
	fixture := &actionWireFake{accept: true}
	path := dbus.ObjectPath("/synthetic/accessibility/node")
	for _, iface := range []string{actionIface, componentIface, editableTextIface, propertiesIface} {
		if exportErr := service.Export(fixture, path, iface); exportErr != nil {
			t.Fatalf("export %s: %v", iface, exportErr)
		}
	}
	id := NodeID{BusName: "org.test.Application", ObjectPath: string(path), Generation: b.Generation(), Incarnation: 1}
	return b, fixture, id
}

func TestATSPIActionComponentAndPropertyMutationsAcrossPrivateDBus(t *testing.T) {
	b, fixture, id := newPrivateATSPIActionWireFixture(t)
	t.Run("success", func(t *testing.T) { assertPrivateATSPIWireSuccess(t, b, fixture, id) })
	t.Run("rejected", func(t *testing.T) { assertPrivateATSPIWireRejected(t, b, fixture, id) })
	t.Run("transport errors", func(t *testing.T) { assertPrivateATSPIWireErrors(t, b, fixture, id) })
	t.Run("metadata errors", func(t *testing.T) { assertPrivateATSPIWireMetadataErrors(t, b, fixture, id) })
}

func assertPrivateATSPIWireSuccess(t *testing.T, b *dbusBackend, fixture *actionWireFake, id NodeID) {
	t.Helper()
	ctx := context.Background()

	chosen, err := b.InvokeDefaultAction(ctx, id)
	if err != nil || chosen.Name != "save" || chosen.Index != 0 || chosen.LocalizedName != "Localized Save" {
		t.Fatalf("first authorized action: %+v err=%v", chosen, err)
	}
	chosen, err = b.InvokeActionByName(ctx, id, "OPEN")
	if err != nil || chosen.Index != 1 || chosen.Name != "open" {
		t.Fatalf("named action: %+v err=%v", chosen, err)
	}
	if err := b.GrabFocus(ctx, id); err != nil {
		t.Fatalf("authorized component focus: %v", err)
	}
	if err := b.CopyText(ctx, id, 0, 2); err != nil {
		t.Fatalf("authorized clipboard copy: %v", err)
	}
	if err := b.SetValue(ctx, id, 1.25); err != nil {
		t.Fatalf("authorized property mutation: %v", err)
	}
	assertPrivateATSPIWireSuccessfulState(t, fixture)
}

func assertPrivateATSPIWireSuccessfulState(t *testing.T, fixture *actionWireFake) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.actions) != 2 || fixture.actions[0] != 0 || fixture.actions[1] != 1 || fixture.focusCalls != 1 || !fixture.copied || len(fixture.values) != 1 || fixture.values[0] != 1.25 {
		t.Fatalf("wire mutation sequence invalid: actions=%v focus=%d copied=%v values=%v", fixture.actions, fixture.focusCalls, fixture.copied, fixture.values)
	}
}

func assertPrivateATSPIWireRejected(t *testing.T, b *dbusBackend, fixture *actionWireFake, id NodeID) {
	t.Helper()
	ctx := context.Background()
	fixture.mu.Lock()
	fixture.accept = false
	fixture.mu.Unlock()

	if err := b.GrabFocus(ctx, id); !errors.Is(err, ErrMutationRejected) {
		t.Fatalf("component rejected false was accepted: %v", err)
	}
	if err := b.InvokeAction(ctx, id, 0); !errors.Is(err, ErrMutationRejected) {
		t.Fatalf("action rejected false was accepted: %v", err)
	}
}

func assertPrivateATSPIWireErrors(t *testing.T, b *dbusBackend, fixture *actionWireFake, id NodeID) {
	t.Helper()
	ctx := context.Background()
	fixture.mu.Lock()
	fixture.unknown = true
	fixture.mu.Unlock()

	if err := b.GrabFocus(ctx, id); err == nil || !strings.Contains(err.Error(), "remote component rejected") {
		t.Fatalf("remote component error lost: %v", err)
	}
	if err := b.CopyText(ctx, id, 0, 2); err == nil || !strings.Contains(err.Error(), "remote clipboard denied") {
		t.Fatalf("remote clipboard error lost: %v", err)
	}
	if err := b.SetValue(ctx, id, 2); err == nil || !strings.Contains(err.Error(), "remote property refused") {
		t.Fatalf("remote property error lost: %v", err)
	}
	if err := b.InvokeAction(ctx, id, 1); err == nil || !strings.Contains(err.Error(), "remote action rejected") {
		t.Fatalf("remote action failure lost: %v", err)
	}
}

func assertPrivateATSPIWireMetadataErrors(t *testing.T, b *dbusBackend, fixture *actionWireFake, id NodeID) {
	t.Helper()
	ctx := context.Background()
	fixture.mu.Lock()
	fixture.metadataDenied = true
	fixture.unknown = false
	fixture.mu.Unlock()

	if _, err := b.InvokeDefaultAction(ctx, id); err == nil || !strings.Contains(err.Error(), "metadata denied") {
		t.Fatalf("denied metadata read accepted: %v", err)
	}
	fixture.mu.Lock()
	fixture.metadataDenied = false
	fixture.nameDenied = true
	fixture.mu.Unlock()
	if _, err := b.InvokeActionByName(ctx, id, "save"); err == nil || !strings.Contains(err.Error(), "name denied") {
		t.Fatalf("denied name read accepted: %v", err)
	}
}
