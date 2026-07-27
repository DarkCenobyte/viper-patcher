package patch

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

var chunkedReplaceMagic = [8]byte{'V', 'C', 'R', 'P', '\r', '\n', 0x1a, 0x01}

const (
	chunkedReplaceThreshold uint64 = 16 << 20
	chunkedDescriptorSize   uint64 = 56
	chunkedHeaderSize       uint64 = 12
)

type chunkedReplaceDescriptor struct {
	offset           uint64
	size             uint64
	compressedLength uint64
	digest           string
	path             string
}

type chunkedReplaceCreationRequest struct {
	ctx              context.Context
	target           fileSnapshot
	outputPath       string
	workDirectory    string
	compressionLevel int
	workers          int
	callback         zstd.ProgressFunc
}

// createChunkedReplace compresses canonical snapshot segments directly through
// positional native reads and reuses the chunk digests calculated during the
// immutable snapshot pass.
func createChunkedReplace(request chunkedReplaceCreationRequest) (createdDifferential, error) {
	targetSize := request.target.Size
	count := int((targetSize + hashutil.ChunkSize - 1) / hashutil.ChunkSize)
	if count == 0 {
		return createdDifferential{}, fmt.Errorf("chunked replace requires a non-empty target")
	}
	workers := request.workers
	if workers < 1 {
		workers = 1
	}
	if workers > count {
		workers = count
	}
	if len(request.target.ChunkDigests) != count {
		return createdDifferential{}, fmt.Errorf("chunked replace target has %d chunk digests, expected %d", len(request.target.ChunkDigests), count)
	}

	target, err := os.Open(request.target.SnapshotPath)
	if err != nil {
		return createdDifferential{}, fmt.Errorf("open chunked replace target: %w", err)
	}
	defer target.Close()

	descriptors := make([]chunkedReplaceDescriptor, count)
	var progressMutex sync.Mutex
	var processed uint64
	prefix := filepath.Base(request.outputPath)
	operationError := parallelFor(request.ctx, count, workers, func(ctx context.Context, index int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		offset := uint64(index) * hashutil.ChunkSize
		length := hashutil.ChunkSize
		if targetSize-offset < length {
			length = targetSize - offset
		}

		placeholder, err := os.CreateTemp(request.workDirectory, fmt.Sprintf(".%s.%06d-*.zst", prefix, index))
		if err != nil {
			return fmt.Errorf("reserve chunked replace output: %w", err)
		}
		compressedPath := placeholder.Name()
		if err := placeholder.Close(); err != nil {
			_ = os.Remove(compressedPath)
			return fmt.Errorf("close reserved chunked replace output: %w", err)
		}
		if err := os.Remove(compressedPath); err != nil {
			return fmt.Errorf("prepare chunked replace output: %w", err)
		}
		if err := zstd.CompressFileSegment(target, offset, length, compressedPath, request.compressionLevel, nil); err != nil {
			_ = os.Remove(compressedPath)
			return err
		}
		compressedSize, err := regularFileSize(compressedPath)
		if err != nil {
			_ = os.Remove(compressedPath)
			return err
		}
		descriptors[index] = chunkedReplaceDescriptor{
			offset:           offset,
			size:             length,
			compressedLength: compressedSize,
			digest:           hex.EncodeToString(request.target.ChunkDigests[index][:]),
			path:             compressedPath,
		}
		if request.callback != nil {
			progressMutex.Lock()
			processed += length
			request.callback(processed, targetSize)
			progressMutex.Unlock()
		}
		return nil
	})
	if operationError != nil {
		for _, descriptor := range descriptors {
			if descriptor.path != "" {
				_ = os.Remove(descriptor.path)
			}
		}
		return createdDifferential{}, operationError
	}

	output, err := os.OpenFile(request.outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		for _, descriptor := range descriptors {
			_ = os.Remove(descriptor.path)
		}
		return createdDifferential{}, fmt.Errorf("create chunked replace differential: %w", err)
	}
	committed := false
	defer func() {
		_ = output.Close()
		for _, descriptor := range descriptors {
			_ = os.Remove(descriptor.path)
		}
		if !committed {
			_ = os.Remove(request.outputPath)
		}
	}()

	if _, err := output.Write(chunkedReplaceMagic[:]); err != nil {
		return createdDifferential{}, err
	}
	if err := binary.Write(output, binary.LittleEndian, uint32(count)); err != nil {
		return createdDifferential{}, err
	}
	for _, descriptor := range descriptors {
		if err := writeChunkedDescriptor(output, descriptor); err != nil {
			return createdDifferential{}, err
		}
	}
	for _, descriptor := range descriptors {
		if err := appendFile(output, descriptor.path); err != nil {
			return createdDifferential{}, err
		}
	}
	if err := output.Close(); err != nil {
		return createdDifferential{}, fmt.Errorf("close chunked replace differential: %w", err)
	}
	compressedSize, err := regularFileSize(request.outputPath)
	if err != nil {
		return createdDifferential{}, err
	}
	committed = true
	return createdDifferential{
		method:         patchformat.MethodChunkedReplace,
		path:           outputPath,
		compressedSize: compressedSize,
	}, nil
}

func writeChunkedDescriptor(writer io.Writer, descriptor chunkedReplaceDescriptor) error {
	if err := binary.Write(writer, binary.LittleEndian, descriptor.offset); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.LittleEndian, descriptor.size); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.LittleEndian, descriptor.compressedLength); err != nil {
		return err
	}
	digest, err := hex.DecodeString(descriptor.digest)
	if err != nil || len(digest) != 32 {
		return fmt.Errorf("invalid chunked replace digest")
	}
	_, err = writer.Write(digest)
	return err
}
