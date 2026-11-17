package accessibility

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

type qtInsertTextCall struct {
	offset int32
	text   string
	length int32
}

type qtInsertTextProvider struct {
	application objectRef
	calls       chan qtInsertTextCall
}

func (p *qtInsertTextProvider) GetApplication() (objectRef, *dbus.Error) {
	return p.application, nil
}

func (p *qtInsertTextProvider) InsertText(offset int32, text string, length int32) (bool, *dbus.Error) {
	p.calls <- qtInsertTextCall{offset: offset, text: text, length: length}
	return true, nil
}

type qtToolkitProperties struct{}

func (qtToolkitProperties) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface != "org.a11y.atspi.Application" || name != "ToolkitName" {
		return dbus.Variant{}, dbus.MakeFailedError(errors.New("unexpected application property read"))
	}
	return dbus.MakeVariant("Qt"), nil
}

// Qt's D-Bus InsertText length is UTF-16 units, so verify the toolkit lookup
// and conversion through the real private-bus mutation call.
func TestInsertTextUsesQtWireLengthOnPrivateDBus(t *testing.T) {
	address := startTestDBus(t)
	service, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(registryName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register private AT-SPI provider: %v %v", reply, err)
	}

	const (
		applicationPath = dbus.ObjectPath("/org/a11y/atspi/accessible/qt_application")
		nodePath        = dbus.ObjectPath("/org/a11y/atspi/accessible/qt_editor")
	)
	application := objectRef{BusName: registryName, ObjectPath: applicationPath}
	provider := &qtInsertTextProvider{application: application, calls: make(chan qtInsertTextCall, 1)}
	for _, iface := range []string{accessibleIface, editableTextIface} {
		if err := service.Export(provider, nodePath, iface); err != nil {
			t.Fatalf("export %s: %v", iface, err)
		}
	}
	if err := service.Export(qtToolkitProperties{}, applicationPath, propertiesIface); err != nil {
		t.Fatalf("export application properties: %v", err)
	}
	client, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	backend := &dbusBackend{generation: 1, access: client, toolkits: make(map[string]string)}
	id := backend.refID(objectRef{BusName: registryName, ObjectPath: nodePath})

	text := "é😀"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := backend.InsertText(ctx, id, 7, text); err != nil {
		t.Fatalf("insert through private AT-SPI provider: %v", err)
	}
	select {
	case call := <-provider.calls:
		if call.offset != 7 || call.text != text || call.length != 3 {
			t.Fatalf("provider received InsertText(%d, %q, %d), want offset 7 and UTF-16 length 3", call.offset, call.text, call.length)
		}
	case <-ctx.Done():
		t.Fatalf("provider did not receive InsertText: %v", ctx.Err())
	}
}
