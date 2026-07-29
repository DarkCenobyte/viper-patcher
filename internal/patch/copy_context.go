//go:build ignore

package patch

import (
	"context"
	"fmt"
	"io"
	"sync"
)

const explicitCopyBufferSize = 1 << 20

type explicitCopyBuffer [explicitCopyBufferSize]byte

var explicitCopyBuffers = sync.Pool{
	New: func() any {
		return new(explicitCopyBuffer)
	},
}

// copyBuffered copies with a guaranteed one MiB userspace buffer. It does not
// delegate to io.WriterTo or io.ReaderFrom, whose implementation-dependent fast
// paths may otherwise select much smaller buffers.
func copyBuffered(destination io.Writer, source io.Reader) (int64, error) {
	return copyContext(context.Background(), destination, source)
}

// copyContext is copyBuffered with cooperative cancellation between reads and
// writes. A block read immediately before cancellation is not written.
func copyContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	buffer := explicitCopyBuffers.Get().(*explicitCopyBuffer)
	defer explicitCopyBuffers.Put(buffer)
	return copyBufferContext(ctx, destination, source, buffer[:])
}

func copyBufferContext(ctx context.Context, destination io.Writer, source io.Reader, buffer []byte) (int64, error) {
	if len(buffer) == 0 {
		return 0, io.ErrShortBuffer
	}
	var written int64
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		readCount, readError := source.Read(buffer)
		if readCount < 0 || readCount > len(buffer) {
			return written, fmt.Errorf("stream reader returned invalid count %d", readCount)
		}
		if readCount > 0 {
			emptyReads = 0
			if err := ctx.Err(); err != nil {
				return written, err
			}
			writeCount, writeError := destination.Write(buffer[:readCount])
			if writeCount < 0 || writeCount > readCount {
				return written, fmt.Errorf("stream writer returned invalid count %d", writeCount)
			}
			written += int64(writeCount)
			if writeError != nil {
				return written, writeError
			}
			if writeCount != readCount {
				return written, io.ErrShortWrite
			}
		} else if readError == nil {
			emptyReads++
			if emptyReads >= 100 {
				return written, io.ErrNoProgress
			}
		}
		if readError != nil {
			if readError == io.EOF {
				return written, nil
			}
			return written, readError
		}
	}
}
