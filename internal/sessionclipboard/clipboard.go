// Package sessionclipboard owns the session-scoped clipboard backend.
package sessionclipboard

import (
	"context"

	"github.com/nskaggs/perfuncted/v2/clipboard"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/internal/util"
)

// Owner is private to the module; a session constructs and closes it.
type Owner struct {
	backend clipboard.Clipboard
	gate    capability.Admission
}

func New(gate capability.Admission, backend clipboard.Clipboard) *Owner {
	return &Owner{backend: backend, gate: gate}
}

// Install is used only during unpublished session construction.
func (o *Owner) Install(backend clipboard.Clipboard) { o.backend = backend }
func (o *Owner) CheckAvailable(operation string) error {
	if o == nil || o.gate == nil {
		return capability.ErrNilSession
	}
	return o.gate.CheckBackend(operation, o.backend)
}
func (o *Owner) Get(ctx context.Context) (string, error) {
	if err := o.CheckAvailable("get"); err != nil {
		return "", err
	}
	return capability.Read(ctx, o.gate, "clipboard", "get", o.backend.Get)
}
func (o *Owner) Set(ctx context.Context, text string) error {
	if err := o.CheckAvailable("set"); err != nil {
		return err
	}
	return capability.Write(ctx, o.gate, "clipboard", "set", func(ctx context.Context) error { return o.backend.Set(ctx, text) })
}
func (o *Owner) Close() error {
	if o == nil || util.IsNil(o.backend) {
		return nil
	}
	return o.backend.Close()
}
