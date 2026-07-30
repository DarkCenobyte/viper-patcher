//go:build !vipr_static_zstd

package nativev4

import "testing"

func TestSystemBuildUsesPortableBLAKE3Fallback(t *testing.T) {
	if backend := BLAKE3Backend(); backend != "portable" {
		t.Fatalf("BLAKE3 backend = %q, want portable", backend)
	}
}
