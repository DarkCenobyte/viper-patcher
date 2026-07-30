//go:build vipr_static_zstd

package nativev4

import (
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/blake3version"
)

func TestStaticBuildUsesOfficialBLAKE3(t *testing.T) {
	if backend := BLAKE3Backend(); backend != "official-"+blake3version.Version {
		t.Fatalf("BLAKE3 backend = %q, want official-%s", backend, blake3version.Version)
	}
}
