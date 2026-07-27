package patch

import (
	"context"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"time"

	"github.com/DarkCenobyte/viper-patcher/internal/buildinfo"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

type differentialBlobs struct {
	forward string
	reverse string
}

type compressedCreationFile struct {
	entry patchformat.FileEntry
	blobs differentialBlobs
}

type createdDifferential struct {
	method         string
	path           string
	compressedSize uint64
	expandedSize   uint64
}

type differentialCreationRequest struct {
	ctx           context.Context
	options       CreateOptions
	snapshot      creationSnapshot
	workDirectory string
	index         int
	fileCount     int
	chunkWorkers  int
	callback      progress.Callback
}

type replacementCreationRequest struct {
	ctx              context.Context
	source           fileSnapshot
	target           fileSnapshot
	workDirectory    string
	index            int
	direction        string
	compressionLevel int
	chunkWorkers     int
	callback         zstd.ProgressFunc
}

type payloadCompressionRequest struct {
	ctx              context.Context
	inputPath        string
	outputPath       string
	method           string
	expandedSize     uint64
	compressionLevel int
	callback         zstd.ProgressFunc
}

func compressCreationInputs(ctx context.Context, options CreateOptions, snapshots []creationSnapshot, workDirectory string, workerBudget int, callback progress.Callback) (patchformat.Header, []differentialBlobs, error) {
	compressed := make([]compressedCreationFile, len(snapshots))
	fileWorkers, perFileWorkers := workerAllocation(workerBudget, len(snapshots))
	err := parallelFor(ctx, len(snapshots), fileWorkers, func(ctx context.Context, index int) error {
		snapshot := snapshots[index]
		chunkWorkers := adaptiveChunkWorkers(perFileWorkers, maxUint64(snapshot.source.Size, snapshot.target.Size))
		if err := ctx.Err(); err != nil {
			return err
		}

		progress.Report(callback, progress.Event{
			FileIndex:  index + 1,
			FileCount:  len(snapshots),
			Path:       snapshot.pair.relativePath,
			Stage:      progress.StageCompressingForward,
			TotalBytes: snapshot.target.Size,
		})

		forward, reverse, err := createPreferredDifferential(differentialCreationRequest{
			ctx:           ctx,
			options:       options,
			snapshot:      snapshot,
			workDirectory: workDirectory,
			index:         index,
			fileCount:     len(snapshots),
			chunkWorkers:  chunkWorkers,
			callback:      callback,
		})
		if err != nil {
			return fmt.Errorf("create differential for %q: %w", snapshot.pair.relativePath, err)
		}

		result := compressedCreationFile{
			entry: patchformat.FileEntry{
				Path:                  snapshot.pair.relativePath,
				SourceHash:            snapshot.source.Hash,
				TargetHash:            snapshot.target.Hash,
				SourceSize:            snapshot.source.Size,
				TargetSize:            snapshot.target.Size,
				ForwardMethod:         forward.method,
				ForwardLength:         forward.compressedSize,
				ForwardExpandedLength: forward.expandedSize,
			},
			blobs: differentialBlobs{forward: forward.path},
		}
		if options.CreateReverse {
			result.entry.ReverseMethod = reverse.method
			result.entry.ReverseLength = reverse.compressedSize
			result.entry.ReverseExpandedLength = reverse.expandedSize
			result.blobs.reverse = reverse.path
		}
		compressed[index] = result
		progress.Report(callback, progress.Event{
			FileIndex:      index + 1,
			FileCount:      len(snapshots),
			Path:           snapshot.pair.relativePath,
			Stage:          progress.StageFilePrepared,
			ProcessedBytes: 1,
			TotalBytes:     1,
		})
		return nil
	})
	if err != nil {
		return patchformat.Header{}, nil, err
	}

	header := newPatchHeader(options, len(snapshots))
	blobs := make([]differentialBlobs, len(snapshots))
	var dataOffset uint64
	for index := range compressed {
		entry := compressed[index].entry
		entry.ForwardOffset = dataOffset
		var ok bool
		dataOffset, ok = checkedAdd(dataOffset, entry.ForwardLength)
		if !ok {
			return patchformat.Header{}, nil, fmt.Errorf("forward differential offsets overflow")
		}
		if options.CreateReverse {
			entry.ReverseOffset = dataOffset
			dataOffset, ok = checkedAdd(dataOffset, entry.ReverseLength)
			if !ok {
				return patchformat.Header{}, nil, fmt.Errorf("reverse differential offsets overflow")
			}
		}
		header.Files = append(header.Files, entry)
		blobs[index] = compressed[index].blobs
	}
	return header, blobs, nil
}

func createPreferredDifferential(request differentialCreationRequest) (createdDifferential, createdDifferential, error) {
	snapshot := request.snapshot
	if snapshot.source.Size == snapshot.target.Size {
		forwardRaw := filepath.Join(request.workDirectory, fmt.Sprintf("%06d.forward.sparse", request.index))
		reverseRaw := ""
		if request.options.CreateReverse {
			reverseRaw = filepath.Join(request.workDirectory, fmt.Sprintf("%06d.reverse.sparse", request.index))
		}
		stats, usable, err := createSparseStreamsOptimized(request.ctx, snapshot.source.SnapshotPath, snapshot.target.SnapshotPath, forwardRaw, reverseRaw, snapshot.target.Size)
		if err != nil {
			return createdDifferential{}, createdDifferential{}, err
		}
		if usable {
			forward, err := compressPreparedPayload(payloadCompressionRequest{
				ctx:              request.ctx,
				inputPath:        forwardRaw,
				outputPath:       filepath.Join(request.workDirectory, fmt.Sprintf("%06d.forward.sparse.zst", request.index)),
				method:           patchformat.MethodSparse,
				expandedSize:     stats.expandedSize,
				compressionLevel: request.options.CompressionLevel,
				callback:         compressionProgress(request.callback, request.index, request.fileCount, snapshot.pair.relativePath, progress.StageCompressingForward, snapshot.target.Size),
			})
			if err != nil {
				return createdDifferential{}, createdDifferential{}, err
			}
			var reverse createdDifferential
			if request.options.CreateReverse {
				progress.Report(request.callback, progress.Event{FileIndex: request.index + 1, FileCount: request.fileCount, Path: snapshot.pair.relativePath, Stage: progress.StageCompressingReverse, TotalBytes: snapshot.source.Size})
				reverse, err = compressPreparedPayload(payloadCompressionRequest{
					ctx:              request.ctx,
					inputPath:        reverseRaw,
					outputPath:       filepath.Join(request.workDirectory, fmt.Sprintf("%06d.reverse.sparse.zst", request.index)),
					method:           patchformat.MethodSparse,
					expandedSize:     stats.expandedSize,
					compressionLevel: request.options.CompressionLevel,
					callback:         compressionProgress(request.callback, request.index, request.fileCount, snapshot.pair.relativePath, progress.StageCompressingReverse, snapshot.source.Size),
				})
				if err != nil {
					return createdDifferential{}, createdDifferential{}, err
				}
			}
			_ = os.Remove(forwardRaw)
			if reverseRaw != "" {
				_ = os.Remove(reverseRaw)
			}
			return forward, reverse, nil
		}
	}

	forward, err := createCopyAddOrReplace(replacementCreationRequest{
		ctx:              request.ctx,
		source:           snapshot.source,
		target:           snapshot.target,
		workDirectory:    request.workDirectory,
		index:            request.index,
		direction:        "forward",
		compressionLevel: request.options.CompressionLevel,
		chunkWorkers:     request.chunkWorkers,
		callback:         compressionProgress(request.callback, request.index, request.fileCount, snapshot.pair.relativePath, progress.StageCompressingForward, snapshot.target.Size),
	})
	if err != nil {
		return createdDifferential{}, createdDifferential{}, err
	}
	var reverse createdDifferential
	if request.options.CreateReverse {
		progress.Report(request.callback, progress.Event{FileIndex: request.index + 1, FileCount: request.fileCount, Path: snapshot.pair.relativePath, Stage: progress.StageCompressingReverse, TotalBytes: snapshot.source.Size})
		reverse, err = createCopyAddOrReplace(replacementCreationRequest{
			ctx:              request.ctx,
			source:           snapshot.target,
			target:           snapshot.source,
			workDirectory:    request.workDirectory,
			index:            request.index,
			direction:        "reverse",
			compressionLevel: request.options.CompressionLevel,
			chunkWorkers:     request.chunkWorkers,
			callback:         compressionProgress(request.callback, request.index, request.fileCount, snapshot.pair.relativePath, progress.StageCompressingReverse, snapshot.source.Size),
		})
		if err != nil {
			return createdDifferential{}, createdDifferential{}, err
		}
	}
	return forward, reverse, nil
}

func createCopyAddOrReplace(request replacementCreationRequest) (createdDifferential, error) {
	copyAddRaw := filepath.Join(request.workDirectory, fmt.Sprintf("%06d.%s.copy-add", request.index, request.direction))
	stats, usable, err := createCopyAddStreamOptimized(request.ctx, request.source.SnapshotPath, request.target.SnapshotPath, copyAddRaw, request.target.Size)
	if err != nil {
		return createdDifferential{}, err
	}
	if usable {
		result, err := compressPreparedPayload(payloadCompressionRequest{
			ctx:              request.ctx,
			inputPath:        copyAddRaw,
			outputPath:       filepath.Join(request.workDirectory, fmt.Sprintf("%06d.%s.copy-add.zst", request.index, request.direction)),
			method:           patchformat.MethodCopyAdd,
			expandedSize:     stats.expandedSize,
			compressionLevel: request.compressionLevel,
			callback:         request.callback,
		})
		_ = os.Remove(copyAddRaw)
		return result, err
	}
	if request.target.Size >= chunkedReplaceThreshold {
		return createChunkedReplace(chunkedReplaceCreationRequest{
			ctx:              request.ctx,
			target:           request.target,
			outputPath:       filepath.Join(request.workDirectory, fmt.Sprintf("%06d.%s.chunked-replace", request.index, request.direction)),
			workDirectory:    request.workDirectory,
			compressionLevel: request.compressionLevel,
			workers:          request.chunkWorkers,
			callback:         request.callback,
		})
	}
	return compressPreparedPayload(payloadCompressionRequest{
		ctx:              request.ctx,
		inputPath:        request.target.SnapshotPath,
		outputPath:       filepath.Join(request.workDirectory, fmt.Sprintf("%06d.%s.replace.zst", request.index, request.direction)),
		method:           patchformat.MethodReplace,
		compressionLevel: request.compressionLevel,
		callback:         request.callback,
	})
}

func sparseWorthUsing(stats sparseStats, size uint64) bool {
	if size == 0 {
		return true
	}
	return stats.changedBytes <= size/8 && stats.expandedSize <= size/4
}

func copyAddWorthUsing(stats copyAddStats, targetSize uint64) bool {
	if targetSize == 0 || stats.copiedBytes < targetSize/8 {
		return false
	}
	limit := targetSize - targetSize/4
	var ok bool
	limit, ok = checkedAdd(limit, 1<<20)
	return ok && stats.expandedSize <= limit
}

func compressPreparedPayload(request payloadCompressionRequest) (createdDifferential, error) {
	if err := request.ctx.Err(); err != nil {
		return createdDifferential{}, err
	}
	if err := zstd.CompressFile(request.inputPath, request.outputPath, request.compressionLevel, request.callback); err != nil {
		return createdDifferential{}, err
	}
	compressedSize, err := regularFileSize(request.outputPath)
	if err != nil {
		return createdDifferential{}, err
	}
	return createdDifferential{
		method:         request.method,
		path:           request.outputPath,
		compressedSize: compressedSize,
		expandedSize:   request.expandedSize,
	}, nil
}

func newPatchHeader(options CreateOptions, capacity int) patchformat.Header {
	return patchformat.Header{
		FormatVersion: patchformat.FormatVersion,
		CreatedAt:     time.Now().UTC(),
		Creator: patchformat.CreatorInfo{
			Name:      "viper-patcher creator",
			Version:   buildinfo.Version,
			Commit:    buildinfo.Commit,
			BuildDate: buildinfo.BuildDate,
		},
		Comment:       options.Comment,
		HashAlgorithm: patchformat.HashBLAKE3Tree,
		Compression: patchformat.Compression{
			Algorithm: patchformat.AlgorithmHybrid,
			Library:   zstd.Version(),
			Mode:      patchformat.CompressionHybrid,
			Level:     options.CompressionLevel,
		},
		Reverse: options.CreateReverse,
		Files:   make([]patchformat.FileEntry, 0, capacity),
	}
}

func compressionProgress(callback progress.Callback, index, count int, path string, stage progress.Stage, logicalTotal uint64) zstd.ProgressFunc {
	return func(processed, total uint64) {
		progress.Report(callback, progress.Event{
			FileIndex:      index + 1,
			FileCount:      count,
			Path:           path,
			Stage:          stage,
			ProcessedBytes: scaleProgress(processed, total, logicalTotal),
			TotalBytes:     logicalTotal,
		})
	}
}

func scaleProgress(processed, total, logicalTotal uint64) uint64 {
	if logicalTotal == 0 || total == 0 || processed >= total {
		return logicalTotal
	}
	high, low := bits.Mul64(processed, logicalTotal)
	value, _ := bits.Div64(high, low, total)
	return value
}

func regularFileSize(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("inspect %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() < 0 {
		return 0, fmt.Errorf("%q is not a regular file", path)
	}
	return uint64(info.Size()), nil
}

func checkedAdd(left, right uint64) (uint64, bool) {
	if left > ^uint64(0)-right {
		return 0, false
	}
	return left + right, true
}
