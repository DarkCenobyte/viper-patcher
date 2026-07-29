//go:build ignore

package patch

import (
	"bytes"
	"context"
	"io"
	"testing"
)

type writerToProbe struct {
	reader        *bytes.Reader
	writeToCalled bool
	firstReadSize int
}

func (probe *writerToProbe) Read(buffer []byte) (int, error) {
	if probe.firstReadSize == 0 {
		probe.firstReadSize = len(buffer)
	}
	return probe.reader.Read(buffer)
}

func (probe *writerToProbe) WriteTo(writer io.Writer) (int64, error) {
	probe.writeToCalled = true
	return probe.reader.WriteTo(writer)
}

func TestCopyBufferedUsesExplicitLargeBuffer(t *testing.T) {
	data := bytes.Repeat([]byte("large-buffer-copy-"), 1<<17)
	source := &writerToProbe{reader: bytes.NewReader(data)}
	var output bytes.Buffer
	written, err := copyBuffered(&output, source)
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(len(data)) || !bytes.Equal(output.Bytes(), data) {
		t.Fatal("copied data mismatch")
	}
	if source.writeToCalled {
		t.Fatal("copyBuffered unexpectedly delegated to io.WriterTo")
	}
	if source.firstReadSize != explicitCopyBufferSize {
		t.Fatalf("first read buffer = %d, want %d", source.firstReadSize, explicitCopyBufferSize)
	}
}

type cancelingReader struct {
	cancel context.CancelFunc
	read   bool
}

func (reader *cancelingReader) Read(buffer []byte) (int, error) {
	if reader.read {
		return 0, io.EOF
	}
	reader.read = true
	count := copy(buffer, []byte("discard-after-cancel"))
	reader.cancel()
	return count, nil
}

func TestCopyContextStopsBeforeWritingCanceledRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var output bytes.Buffer
	written, err := copyContext(ctx, &output, &cancelingReader{cancel: cancel})
	if err != context.Canceled {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if written != 0 || output.Len() != 0 {
		t.Fatalf("wrote %d bytes after cancellation", written)
	}
}

func TestCopyBufferContextRejectsEmptyBuffer(t *testing.T) {
	if _, err := copyBufferContext(context.Background(), io.Discard, bytes.NewReader(nil), nil); err != io.ErrShortBuffer {
		t.Fatalf("error = %v, want io.ErrShortBuffer", err)
	}
}
