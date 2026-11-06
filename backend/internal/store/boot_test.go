package store

import "testing"

func TestCurrentBootIdentityStableWhenAvailable(t *testing.T) {
	first := currentBootIdentity()
	second := currentBootIdentity()
	if first.host == "" || first.boot == "" {
		t.Skip("host or boot identity unavailable")
	}
	if first != second {
		t.Fatal("host or boot identity changed during one process")
	}
}
