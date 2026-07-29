package patch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCopyAddHashStartSkipsOnlyNonDecisionPrefix(t *testing.T) {
	tests := []struct {
		name    string
		profile copyAddChunkProfile
		want    int
	}{
		{
			name: "default",
			profile: copyAddChunkProfile{
				minimum: copyAddChunkDefaultMin,
				average: copyAddChunkDefaultAvg,
				maximum: copyAddChunkDefaultMax,
			},
			want: copyAddChunkDefaultMin - 14,
		},
		{
			name: "largest",
			profile: copyAddChunkProfile{
				minimum: copyAddChunkLargestAvg / 4,
				average: copyAddChunkLargestAvg,
				maximum: copyAddChunkLargestMax,
			},
			want: copyAddChunkLargestAvg/4 - 22,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := copyAddHashStart(test.profile); got != test.want {
				t.Fatalf("hash start = %d, want %d", got, test.want)
			}
		})
	}
}

func TestCopyAddCutPointSkippingPreservesBoundaries(t *testing.T) {
	data := make([]byte, 20<<20+12345)
	state := uint64(0x243f6a8885a308d3)
	for index := range data {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		data[index] = byte(state)
	}

	path := filepath.Join(t.TempDir(), "chunking.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	profiles := []copyAddChunkProfile{
		{minimum: 4 << 10, average: 16 << 10, maximum: 64 << 10},
		{minimum: 64 << 10, average: 256 << 10, maximum: 1 << 20},
		{minimum: 1 << 20, average: 4 << 20, maximum: 16 << 20},
	}
	for _, profile := range profiles {
		var actual []int
		if err := forEachContentChunk(context.Background(), file, profile, func(chunk contentChunk) error {
			actual = append(actual, len(chunk.data))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		expected := legacyCopyAddChunkLengths(data, profile)
		if !slices.Equal(actual, expected) {
			t.Fatalf("profile %+v changed chunk boundaries\nactual: %v\nexpected: %v", profile, actual, expected)
		}
	}
}

func legacyCopyAddChunkLengths(data []byte, profile copyAddChunkProfile) []int {
	var lengths []int
	var gearHash uint64
	chunkLength := 0
	for _, value := range data {
		chunkLength++
		gearHash = (gearHash << 1) + copyAddGearTable[value]
		if chunkLength < profile.minimum {
			continue
		}
		if chunkLength < profile.maximum && gearHash&uint64(profile.average-1) != 0 {
			continue
		}
		lengths = append(lengths, chunkLength)
		chunkLength = 0
		gearHash = 0
	}
	if chunkLength > 0 {
		lengths = append(lengths, chunkLength)
	}
	return lengths
}

func TestSelectCopyAddCandidatePrefersPendingCopyContinuation(t *testing.T) {
	candidates := indexedChunkCandidates{
		count: 4,
		chunks: [copyAddMaxCandidates]indexedChunk{
			{offset: 512, length: 64},
			{offset: 128, length: 32},
			{offset: 96, length: 64},
			{offset: 4096, length: 64},
		},
	}
	stream := &copyAddStreamWriter{copyOffset: 32, copyLength: 64}
	candidate, matched := selectCopyAddCandidate(candidates, 64, stream)
	if !matched || candidate.offset != 96 {
		t.Fatalf("candidate = %+v, matched = %v", candidate, matched)
	}
	if err := stream.copy(candidate.offset, uint64(candidate.length)); err != nil {
		t.Fatal(err)
	}
	if stream.copyOffset != 32 || stream.copyLength != 128 {
		t.Fatalf("merged COPY = offset %d length %d", stream.copyOffset, stream.copyLength)
	}
}

func TestSelectCopyAddCandidateKeepsFirstMatchFallback(t *testing.T) {
	candidates := indexedChunkCandidates{
		count: 3,
		chunks: [copyAddMaxCandidates]indexedChunk{
			{offset: 10, length: 32},
			{offset: 20, length: 64},
			{offset: 30, length: 64},
		},
	}
	candidate, matched := selectCopyAddCandidate(candidates, 64, &copyAddStreamWriter{})
	if !matched || candidate.offset != 20 {
		t.Fatalf("candidate = %+v, matched = %v", candidate, matched)
	}
}
