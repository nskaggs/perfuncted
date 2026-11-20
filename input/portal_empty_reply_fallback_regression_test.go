package input

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type emptyReplyPortal struct {
	conn *dbus.Conn
	code uint32
}

func (p *emptyReplyPortal) CreateSession(sender dbus.Sender, options map[string]dbus.Variant) (string, *dbus.Error) {
	token, ok := options["handle_token"].Value().(string)
	if !ok {
		return "", dbus.MakeFailedError(errors.New("missing request token"))
	}
	path := portalRequestPath(string(sender), token)
	results := map[string]dbus.Variant{"session_handle": dbus.MakeVariant(syntheticSessionPath)}
	if err := p.conn.Emit(path, portalRequestInterface+".Response", p.code, results); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return "", nil
}

func TestPortalEmptyStringReplyUsesPredictedRequestPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		code uint32
		want error
	}{
		{name: "authorized", code: 0},
		{name: "denied", code: 1, want: ErrRemoteDesktopDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, portal := startSyntheticPortalBus(t)
			if err := portal.conn.Export(&emptyReplyPortal{conn: portal.conn, code: tc.code}, remoteDesktopPath, remoteDesktopInterface); err != nil {
				t.Fatal(err)
			}
			name, err := portalUniqueName(client.Names())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			handle, err := portalCreateSession(ctx, client, name)
			if tc.want != nil {
				if handle != "" || !errors.Is(err, tc.want) {
					t.Fatalf("denied response returned handle=%q err=%v", handle, err)
				}
			} else if err != nil || handle != syntheticSessionPath {
				t.Fatalf("authorized response returned handle=%q err=%v", handle, err)
			}
		})
	}
}
