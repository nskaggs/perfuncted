// Package ctxutil provides context utilities.
package ctxutil

import "context"

// Default returns ctx if non-nil, otherwise context.Background().
// Use this at the top of public functions to guarantee a non-nil context
// without panicking on nil.
func Default(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
