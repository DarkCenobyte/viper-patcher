package patch

import (
	"bytes"
	"sort"
	"unsafe"
)

type copyAddIndexEntry struct {
	digest     [32]byte
	candidates indexedChunkCandidates
}

var copyAddIndexEntrySize = uint64(unsafe.Sizeof(copyAddIndexEntry{}))

type copyAddIndex struct {
	entries []copyAddIndexEntry
}

func newCopyAddIndex(capacity int) *copyAddIndex {
	if capacity < 0 {
		capacity = 0
	}
	return &copyAddIndex{entries: make([]copyAddIndexEntry, 0, capacity)}
}

func (index *copyAddIndex) add(digest [32]byte, chunk indexedChunk) error {
	if len(index.entries) == cap(index.entries) {
		return errCopyAddIndexBudgetExceeded
	}
	var candidates indexedChunkCandidates
	candidates.add(chunk)
	index.entries = append(index.entries, copyAddIndexEntry{digest: digest, candidates: candidates})
	return nil
}

func (index *copyAddIndex) finalize() {
	sort.SliceStable(index.entries, func(left, right int) bool {
		return bytes.Compare(index.entries[left].digest[:], index.entries[right].digest[:]) < 0
	})
	writeIndex := 0
	for readIndex := range index.entries {
		entry := index.entries[readIndex]
		if writeIndex > 0 && index.entries[writeIndex-1].digest == entry.digest {
			for candidateIndex := 0; candidateIndex < int(entry.candidates.count); candidateIndex++ {
				index.entries[writeIndex-1].candidates.add(entry.candidates.chunks[candidateIndex])
			}
			continue
		}
		index.entries[writeIndex] = entry
		writeIndex++
	}
	index.entries = index.entries[:writeIndex]
}

func (index *copyAddIndex) candidates(digest [32]byte) indexedChunkCandidates {
	position := sort.Search(len(index.entries), func(position int) bool {
		return bytes.Compare(index.entries[position].digest[:], digest[:]) >= 0
	})
	if position >= len(index.entries) || index.entries[position].digest != digest {
		return indexedChunkCandidates{}
	}
	return index.entries[position].candidates
}
