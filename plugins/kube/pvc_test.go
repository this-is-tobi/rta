package main

import "testing"

func TestQuantityBytes(t *testing.T) {
	if got := quantityBytes("8Gi"); got != "8.0 GiB" {
		t.Errorf("quantityBytes(8Gi) = %q, want 8.0 GiB", got)
	}
	// Pending PVCs have no status.capacity yet — "" is the honest answer,
	// not "0 B", which would read as an empty volume rather than an unknown
	// one.
	if got := quantityBytes(""); got != "" {
		t.Errorf("quantityBytes(\"\") = %q, want empty", got)
	}
}
