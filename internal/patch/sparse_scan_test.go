package patch

import (
	"bytes"
	"testing"
)

func TestNextSparseDifferenceSkipsEqualBlocksAndPreservesRuns(t *testing.T) {
	source := bytes.Repeat([]byte{0x2a}, 3*sparseComparisonBlockSize+17)
	target := append([]byte(nil), source...)
	target[sparseComparisonBlockSize-1] = 0x10
	target[sparseComparisonBlockSize] = 0x11
	target[2*sparseComparisonBlockSize+5] = 0x12

	var ranges [][2]int
	for index := 0; index < len(source); {
		start, end, found := nextSparseDifference(source, target, index)
		if !found {
			break
		}
		ranges = append(ranges, [2]int{start, end})
		index = end
	}
	want := [][2]int{
		{sparseComparisonBlockSize - 1, sparseComparisonBlockSize + 1},
		{2*sparseComparisonBlockSize + 5, 2*sparseComparisonBlockSize + 6},
	}
	if len(ranges) != len(want) {
		t.Fatalf("ranges = %#v, want %#v", ranges, want)
	}
	for index := range want {
		if ranges[index] != want[index] {
			t.Fatalf("range %d = %v, want %v", index, ranges[index], want[index])
		}
	}
}

func TestNextSparseDifferenceReportsNoChange(t *testing.T) {
	data := bytes.Repeat([]byte{0x7f}, sparseComparisonBlockSize+1)
	if _, _, found := nextSparseDifference(data, data, 0); found {
		t.Fatal("identical data unexpectedly reported a difference")
	}
}
