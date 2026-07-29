//go:build ignore

package patch

import (
	"bytes"
	"context"
	"errors"
	"io"
)

const instructionMemoryThreshold uint64 = 2 << 20

// applyCompressedInstructionStream keeps small instruction streams in memory
// and switches to a bounded pipe for larger streams shared by sparse and
// COPY/ADD application.
func applyCompressedInstructionStream(ctx context.Context, decoder *decoderLease, expandedLength uint64, apply func(io.Reader) error) error {
	if expandedLength <= instructionMemoryThreshold {
		var buffer bytes.Buffer
		buffer.Grow(int(expandedLength))
		if err := decoder.DecompressToWriter(ctx, &buffer, expandedLength, nil); err != nil {
			return err
		}
		return apply(bytes.NewReader(buffer.Bytes()))
	}

	decodeContext, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	decodeDone := make(chan error, 1)
	go func() {
		err := decoder.DecompressToWriter(decodeContext, writer, expandedLength, nil)
		_ = writer.CloseWithError(err)
		decodeDone <- err
	}()

	applyError := apply(reader)
	if applyError != nil {
		cancel()
		_ = reader.CloseWithError(applyError)
	} else {
		_ = reader.Close()
	}
	decodeError := <-decodeDone
	if applyError == nil {
		return decodeError
	}
	if decodeError == nil || errors.Is(decodeError, context.Canceled) || errors.Is(decodeError, io.ErrClosedPipe) || errors.Is(applyError, decodeError) {
		return applyError
	}
	return errors.Join(applyError, decodeError)
}
