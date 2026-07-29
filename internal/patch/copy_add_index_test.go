//go:build ignore

package patch

import "testing"

func TestCopyAddIndexCompactsDuplicateDigests(t *testing.T) {
	index := newCopyAddIndex(8)
	var digest [32]byte
	digest[0] = 1
	for offset := uint64(0); offset < copyAddMaxCandidates+2; offset++ {
		if err := index.add(digest, indexedChunk{offset: offset, length: 4}); err != nil {
			t.Fatal(err)
		}
	}
	index.finalize()
	candidates := index.candidates(digest)
	if len(index.entries) != 1 || int(candidates.count) != copyAddMaxCandidates {
		t.Fatalf("index=%+v candidates=%+v", index.entries, candidates)
	}
}
