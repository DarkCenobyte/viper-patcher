package patch

import (
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestSourceCachePolicyIsHDDOnly(t *testing.T) {
	windows := []patchformat.WindowDescriptor{{
		Kind:       patchformat.WindowCopy,
		SourceSize: 16 << 20,
	}}
	for _, profile := range []IOProfile{IOAuto, IOSSD, IONVMe} {
		if sourceCacheWorthTrying(profile, windows, 64<<20) {
			t.Fatalf("source cache unexpectedly enabled for %s", profile)
		}
	}
	if !sourceCacheWorthTrying(IOHDD, windows, 64<<20) {
		t.Fatal("source cache should be enabled for a copy-heavy HDD workload")
	}
}

func TestSourceCachePolicyRejectsSmallAndOversizedReferences(t *testing.T) {
	small := []patchformat.WindowDescriptor{{
		Kind:       patchformat.WindowCopy,
		SourceSize: 1 << 20,
	}}
	if sourceCacheWorthTrying(IOHDD, small, 64<<20) {
		t.Fatal("source cache enabled for a negligible referenced range")
	}

	copyHeavy := []patchformat.WindowDescriptor{{
		Kind:       patchformat.WindowDeltaRaw,
		SourceSize: 64 << 20,
	}}
	if sourceCacheWorthTrying(IOHDD, copyHeavy, 257<<20) {
		t.Fatal("source cache enabled above the 64-bit size ceiling")
	}
}
