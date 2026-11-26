// Package sessionoutput owns the session-scoped display discovery backend.
package sessionoutput

import (
	"context"
	"errors"

	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"github.com/nskaggs/perfuncted/v2/output"
)

type Owner struct {
	backend output.Lister
	gate    capability.Admission
}

func New(gate capability.Admission, backend output.Lister) *Owner {
	return &Owner{backend: backend, gate: gate}
}
func (o *Owner) Install(backend output.Lister) { o.backend = backend }
func (o *Owner) Backend() output.Lister {
	if o == nil {
		return nil
	}
	return o.backend
}
func (o *Owner) CheckAvailable(operation string) error {
	if o == nil || o.gate == nil {
		return capability.ErrNilSession
	}
	return o.gate.CheckBackend(operation, o.backend)
}
func (o *Owner) List(ctx context.Context) ([]output.Info, error) {
	if err := o.CheckAvailable("list"); err != nil {
		return nil, err
	}
	return capability.Read(ctx, o.gate, "output", "list", o.backend.List)
}

// DiagnosticList preserves the separate diagnostic read contract. Session
// chooses the diagnostic timeout and persistence, while this owner reads.
func (o *Owner) DiagnosticList(ctx context.Context) ([]output.Info, error) {
	if o == nil || util.IsNil(o.backend) {
		return nil, errors.New("output capability is not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values, err := o.backend.List(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return values, err
}
func (o *Owner) Close() error {
	if o == nil || util.IsNil(o.backend) {
		return nil
	}
	return o.backend.Close()
}
