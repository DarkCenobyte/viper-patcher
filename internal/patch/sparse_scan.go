package patch

import "bytes"

const sparseComparisonBlockSize = 256

// nextSparseDifference skips equal regions in optimized blocks and refines only
// blocks known to differ. It preserves exact runs of consecutive unequal bytes.
func nextSparseDifference(source, target []byte, start int) (int, int, bool) {
	for index := start; index < len(source); {
		blockEnd := index + sparseComparisonBlockSize
		if blockEnd > len(source) {
			blockEnd = len(source)
		}
		if bytes.Equal(source[index:blockEnd], target[index:blockEnd]) {
			index = blockEnd
			continue
		}
		for index < blockEnd && source[index] == target[index] {
			index++
		}
		differenceStart := index
		for index < len(source) && source[index] != target[index] {
			index++
		}
		return differenceStart, index, true
	}
	return 0, 0, false
}
