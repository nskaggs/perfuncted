# Pinned local XGB transport correction

Source: github.com/jezek/xgb v1.3.1, Git commit `a225c310b6db5bd387e0c7b7a555711767d5ffa6` (module cache origin).
Unmodified source and BSD-style LICENSE are retained. xgb.go, conn.go and xproto/xproto.go are altered to read the full length-delimited GenericEvent payload (at most 1 MiB), and preserve the complete wire frame in GeGenericEvent.Raw. Non-GenericEvent wire framing and checked/unchecked reply/error matching are unchanged; abort-aware event dispatch and cookie waiting are described below. No upstream contribution is authorized.


Issue #128 follow-up: a context-bound connection constructor and idempotent
force-abort method are defined in xgb.go. conn.go uses the original Xauthority
and X11 handshake with context-aware dial and a five-second setup deadline;
abort closes the socket without waiting for a round trip. The shared event
reader now stops on abort even when dispatch/backpressure or cookie reception
would otherwise block. Existing graceful Close semantics remain unchanged.

Packaging: the patched source now lives inside Perfuncted's main module at
internal/third_party/xgb. Go import paths and generator output target that
internal package; the nested go.mod and root replace directive are removed.
This retains the original license and transport patches and makes the library
available in the distributed Go module. The upstream live xproto tests require
an explicit xgb_upstream_integration build tag and isolated X server.

The main-module vet gate also exposed three unreachable statements and one
self-assignment in the retained upstream code. Removed these no-op statements;
no transport behavior changed.
