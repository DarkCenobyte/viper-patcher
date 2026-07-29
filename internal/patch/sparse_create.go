package patch

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
)

// createSparseStreamsOptimized builds forward and optional reverse sparse
// instruction streams while abandoning the candidate as soon as it exceeds the
// configured usefulness thresholds.
func createSparseStreamsOptimized(ctx context.Context, sourcePath, targetPath, forwardPath, reversePath string, size uint64) (sparseStats, bool, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return sparseStats{}, false, fmt.Errorf("open sparse source: %w", err)
	}
	defer source.Close()
	target, err := os.Open(targetPath)
	if err != nil {
		return sparseStats{}, false, fmt.Errorf("open sparse target: %w", err)
	}
	defer target.Close()

	forward, err := os.OpenFile(forwardPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return sparseStats{}, false, fmt.Errorf("create forward sparse stream: %w", err)
	}
	var reverse *os.File
	committed := false
	defer func() {
		_ = forward.Close()
		if reverse != nil {
			_ = reverse.Close()
		}
		if !committed {
			_ = os.Remove(forwardPath)
			if reversePath != "" {
				_ = os.Remove(reversePath)
			}
		}
	}()

	if reversePath != "" {
		reverse, err = os.OpenFile(reversePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return sparseStats{}, false, fmt.Errorf("create reverse sparse stream: %w", err)
		}
	}

	forwardWriter := bufio.NewWriterSize(forward, sparseIOBufferSize)
	if _, err := forwardWriter.Write(sparseMagic[:]); err != nil {
		return sparseStats{}, false, err
	}
	var reverseWriter *bufio.Writer
	if reverse != nil {
		reverseWriter = bufio.NewWriterSize(reverse, sparseIOBufferSize)
		if _, err := reverseWriter.Write(sparseMagic[:]); err != nil {
			return sparseStats{}, false, err
		}
	}

	stats := sparseStats{expandedSize: uint64(len(sparseMagic))}
	if !sparseWorthUsing(stats, size) && size != 0 {
		return stats, false, nil
	}

	sourceBuffer := make([]byte, sparseIOBufferSize)
	targetBuffer := make([]byte, sparseIOBufferSize)
	var absoluteOffset uint64
	var previousEnd uint64
	for {
		if err := ctx.Err(); err != nil {
			return sparseStats{}, false, err
		}
		sourceCount, sourceError := io.ReadFull(source, sourceBuffer)
		targetCount, targetError := io.ReadFull(target, targetBuffer)
		if sourceError == io.ErrUnexpectedEOF {
			sourceError = nil
		}
		if targetError == io.ErrUnexpectedEOF {
			targetError = nil
		}
		if sourceError != nil && sourceError != io.EOF {
			return sparseStats{}, false, fmt.Errorf("read sparse source: %w", sourceError)
		}
		if targetError != nil && targetError != io.EOF {
			return sparseStats{}, false, fmt.Errorf("read sparse target: %w", targetError)
		}
		if sourceCount != targetCount {
			return sparseStats{}, false, fmt.Errorf("sparse method requires equal source and target sizes")
		}
		if sourceCount == 0 {
			break
		}

		for index := 0; index < sourceCount; {
			start, end, found := nextSparseDifference(sourceBuffer[:sourceCount], targetBuffer[:targetCount], index)
			if !found {
				break
			}
			index = end
			length := index - start
			startOffset := absoluteOffset + uint64(start)
			gap := startOffset - previousEnd
			nextStats := stats
			nextStats.changedBytes += uint64(length)
			nextStats.expandedSize += uint64(uvarintLength(gap) + uvarintLength(uint64(length)) + length)
			if !sparseWorthUsing(nextStats, size) {
				return nextStats, false, nil
			}
			if err := writeSparseRecord(forwardWriter, gap, targetBuffer[start:index]); err != nil {
				return sparseStats{}, false, fmt.Errorf("write forward sparse record: %w", err)
			}
			if reverseWriter != nil {
				if err := writeSparseRecord(reverseWriter, gap, sourceBuffer[start:index]); err != nil {
					return sparseStats{}, false, fmt.Errorf("write reverse sparse record: %w", err)
				}
			}
			previousEnd = startOffset + uint64(length)
			stats = nextStats
		}
		absoluteOffset += uint64(sourceCount)
		if sourceError == io.EOF || targetError == io.EOF {
			break
		}
	}

	stats.expandedSize += 2
	if !sparseWorthUsing(stats, size) {
		return stats, false, nil
	}
	if err := writeSparseTerminator(forwardWriter); err != nil {
		return sparseStats{}, false, err
	}
	if err := forwardWriter.Flush(); err != nil {
		return sparseStats{}, false, err
	}
	if err := forward.Close(); err != nil {
		return sparseStats{}, false, err
	}
	if reverseWriter != nil {
		if err := writeSparseTerminator(reverseWriter); err != nil {
			return sparseStats{}, false, err
		}
		if err := reverseWriter.Flush(); err != nil {
			return sparseStats{}, false, err
		}
		if err := reverse.Close(); err != nil {
			return sparseStats{}, false, err
		}
	}
	committed = true
	return stats, true, nil
}
