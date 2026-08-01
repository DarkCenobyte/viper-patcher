package patch

import (
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func deltaWindow(offset uint64, size uint32) patchformat.WindowDescriptor {
	return patchformat.WindowDescriptor{
		Kind:         patchformat.WindowDeltaRaw,
		SourceOffset: offset,
		SourceSize:   size,
	}
}

func TestFineVerificationPlannerIgnoresSameAndCopy(t *testing.T) {
	windows := []patchformat.WindowDescriptor{
		{Kind: patchformat.WindowSame, SourceSize: 256 << 10},
		{Kind: patchformat.WindowCopy, SourceOffset: 32 << 20, SourceSize: 256 << 10},
	}
	if plan := planFineVerification(64<<20, windows, OptimizeApplySpeed); plan.ChunkSize != 0 {
		t.Fatalf("unexpected fine verification plan: %#v", plan)
	}
}

func TestFineVerificationPlannerSelects64KForSparseApplySpeed(t *testing.T) {
	windows := []patchformat.WindowDescriptor{
		deltaWindow(1<<20, 64<<10),
		deltaWindow(17<<20, 64<<10),
		deltaWindow(33<<20, 64<<10),
		deltaWindow(49<<20, 64<<10),
	}
	plan := planFineVerification(64<<20, windows, OptimizeApplySpeed)
	if plan.ChunkSize != 64<<10 {
		t.Fatalf("fine chunk size = %d, want 64 KiB", plan.ChunkSize)
	}
	if len(plan.Indexes) != 4 || plan.FineBytes != 4*(64<<10) {
		t.Fatalf("unexpected sparse plan: %#v", plan)
	}
	if plan.CanonicalBytes != 4*patchformat.IdentityChunkSize {
		t.Fatalf("canonical bytes = %d", plan.CanonicalBytes)
	}
}

func TestFineVerificationPlannerUses256KForBalancedSparseRanges(t *testing.T) {
	windows := []patchformat.WindowDescriptor{
		deltaWindow(1<<20, 64<<10),
		deltaWindow(17<<20, 64<<10),
	}
	plan := planFineVerification(32<<20, windows, OptimizeBalanced)
	if plan.ChunkSize != 256<<10 {
		t.Fatalf("fine chunk size = %d, want 256 KiB", plan.ChunkSize)
	}
}

func TestFineVerificationPlannerRejectsDenseCoverage(t *testing.T) {
	windows := []patchformat.WindowDescriptor{
		deltaWindow(0, 8<<20),
		deltaWindow(8<<20, 8<<20),
	}
	if plan := planFineVerification(16<<20, windows, OptimizeApplySpeed); plan.ChunkSize != 0 {
		t.Fatalf("dense file unexpectedly received fine verification: %#v", plan)
	}
}

func TestRetainPortableFineVerificationCapsTheWholePatch(t *testing.T) {
	entry := patchformat.FileEntry{
		SourceFineChunkSize: 64 << 10,
		SourceFineChunks:    make([]patchformat.FineDigest, 2),
	}
	total := maxPortableFineDigests - 1
	retainPortableFineVerification(&entry, &total)
	if entry.SourceFineChunkSize != 0 || len(entry.SourceFineChunks) != 0 {
		t.Fatal("fine table exceeding the portable global limit was retained")
	}
	if total != maxPortableFineDigests-1 {
		t.Fatalf("discarded table changed total to %d", total)
	}

	entry.SourceFineChunkSize = 64 << 10
	entry.SourceFineChunks = make([]patchformat.FineDigest, 2)
	total = 0
	retainPortableFineVerification(&entry, &total)
	if total != 2 || len(entry.SourceFineChunks) != 2 {
		t.Fatal("small fine table was not retained")
	}
}
