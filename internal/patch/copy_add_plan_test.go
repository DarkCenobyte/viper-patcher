//go:build ignore

package patch

import "testing"

func TestCopyAddProfileScalesForHugeFiles(t *testing.T) {
	small := copyAddProfileForSize(64 << 20)
	huge := copyAddProfileForSize(512 << 30)
	if small.minimum != copyAddChunkDefaultMin || small.average != copyAddChunkDefaultAvg || small.maximum != copyAddChunkDefaultMax {
		t.Fatalf("small profile = %+v", small)
	}
	if huge.average <= small.average {
		t.Fatalf("huge average = %d, small average = %d", huge.average, small.average)
	}
	if !small.indexable || !huge.indexable {
		t.Fatalf("expected tested profiles to remain indexable: small=%+v huge=%+v", small, huge)
	}
	if huge.average > copyAddChunkLargestAvg || huge.maximum > copyAddChunkLargestMax {
		t.Fatalf("huge profile exceeds limits: %+v", huge)
	}
	if huge.average&(huge.average-1) != 0 {
		t.Fatalf("average chunk size must remain a power of two: %d", huge.average)
	}
	if huge.maxIndexEntries != int(copyAddIndexMemoryBudget/copyAddIndexEntrySize) {
		t.Fatalf("unexpected index entry budget %d", huge.maxIndexEntries)
	}
}

func TestCopyAddIndexCapacityIsBounded(t *testing.T) {
	profile := copyAddProfileForSize(1 << 40)
	capacity := copyAddIndexCapacity(1<<40, profile)
	if capacity > profile.maxIndexEntries {
		t.Fatalf("capacity = %d, maximum = %d", capacity, profile.maxIndexEntries)
	}
}

func TestCopyAddProfileSkipsImpracticallyLargeIndexes(t *testing.T) {
	entryBudget := uint64(copyAddIndexMemoryBudget / copyAddIndexEntrySize)
	tooLarge := entryBudget*uint64(copyAddChunkLargestAvg) + 1
	profile := copyAddProfileForSize(tooLarge)
	if profile.indexable {
		t.Fatalf("profile should fall back instead of indexing %d bytes: %+v", tooLarge, profile)
	}
	if capacity := copyAddIndexCapacity(tooLarge, profile); capacity != 0 {
		t.Fatalf("disabled profile capacity = %d", capacity)
	}
}

func TestIndexedChunkCandidatesUseFixedCapacity(t *testing.T) {
	var candidates indexedChunkCandidates
	for index := 0; index < copyAddMaxCandidates+2; index++ {
		candidates.add(indexedChunk{offset: uint64(index), length: 1})
	}
	if int(candidates.count) != copyAddMaxCandidates {
		t.Fatalf("candidate count = %d", candidates.count)
	}
	if candidates.chunks[copyAddMaxCandidates-1].offset != copyAddMaxCandidates-1 {
		t.Fatalf("last candidate offset = %d", candidates.chunks[copyAddMaxCandidates-1].offset)
	}
}
