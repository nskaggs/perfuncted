// Package contextutil contains implementation-only context helpers.
package contextutil

import "context"

// Default returns ctx when non-nil and a background context otherwise.
func Default(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
