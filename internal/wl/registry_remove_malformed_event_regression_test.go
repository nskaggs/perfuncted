package wl

import "testing"

// A truncated global_remove event must not mutate the compositor's advertised
// interface inventory. A later complete event must still revoke the global.
func TestRegistryGlobalRemoveIgnoresTruncatedEventsAndAcceptsValidRemoval(t *testing.T) {
	s := &Session{}
	r := &Registry{}
	r.SetGlobalHandler(s.addGlobal)
	r.SetGlobalRemoveHandler(s.removeGlobal)
	r.Dispatch(0, -1, registryGlobalData(17, "wl_output", 3))
	if got := s.GlobalsSnapshot(); len(got) != 1 || got[0].Name != 17 {
		t.Fatalf("expected initial advertised output, got %+v", got)
	}
	for _, malformed := range [][]byte{nil, {}, {17}, {17, 0}, {17, 0, 0}} {
		r.Dispatch(1, -1, malformed)
		if got := s.GlobalsSnapshot(); len(got) != 1 || got[0].Name != 17 {
			t.Fatalf("truncated event %v revoked output: %+v", malformed, got)
		}
	}
	r.Dispatch(2, -1, []byte{17, 0, 0, 0})
	if got := s.GlobalsSnapshot(); len(got) != 1 {
		t.Fatalf("unrecognized removal opcode revoked output: %+v", got)
	}
	r.Dispatch(1, -1, []byte{17, 0, 0, 0})
	if got := s.GlobalsSnapshot(); len(got) != 0 {
		t.Fatalf("complete removal event did not revoke output: %+v", got)
	}
}
