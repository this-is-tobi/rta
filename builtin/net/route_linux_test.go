package net

import "testing"

// The live read, against this machine's own tables: it must read them without an
// error even where there is no IPv6 table at all, as on a host booted without IPv6.
func TestTheLiveDefaultRouteIsReadWithoutAnError(t *testing.T) {
	if _, _, err := readDefaultRoute(); err != nil {
		t.Fatal(err)
	}
}
