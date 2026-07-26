package patch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DarkCenobyte/viper-patcher/internal/buildinfo"
	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
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

func compressCreationInputs(ctx context.Context, options CreateOptions, snapshots []creationSnapshot, workDirectory string, parallelism int, callback progress.Callback) (patchformat.Header, []differentialBlobs, error) {
	emptyReference := filepath.Join(workDirectory, "empty-reference")
	if err := os.WriteFile(emptyReference, nil, 0o600); err != nil {
		return patchformat.Header{}, nil, fmt.Errorf("create empty compression reference: %w", err)
	}

	compressed := make([]compressedCreationFile, len(snapshots))
	err := parallelFor(ctx, len(snapshots), parallelism, func(ctx context.Context, index int) error {
		snapshot := snapshots[index]
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

		forward, reverse, err := createBestDifferentials(ctx, options, snapshot, workDirectory, emptyReference, index, callback, len(snapshots))
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
				SourceMode:            snapshot.source.Mode,
				TargetMode:            snapshot.target.Mode,
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

func createBestDifferentials(ctx context.Context, options CreateOptions, snapshot creationSnapshot, workDirectory, emptyReference string, index int, callback progress.Callback, fileCount int) (createdDifferential, createdDifferential, error) {
	if snapshot.source.Size == snapshot.target.Size {
		forwardRaw := filepath.Join(workDirectory, fmt.Sprintf("%06d.forward.sparse", index))
		reverseRaw := ""
		if options.CreateReverse {
			reverseRaw = filepath.Join(workDirectory, fmt.Sprintf("%06d.reverse.sparse", index))
		}
		stats, usable, err := createSparseStreamsOptimized(ctx, snapshot.source.SnapshotPath, snapshot.target.SnapshotPath, forwardRaw, reverseRaw, snapshot.target.Size)
		if err != nil {
			return createdDifferential{}, createdDifferential{}, err
		}
		if usable {
			forward, err := compressPreparedPayload(ctx, emptyReference, forwardRaw, filepath.Join(workDirectory, fmt.Sprintf("%06d.forward.sparse.zst", index)), patchformat.MethodSparse, stats.expandedSize, options.CompressionLevel, compressionProgress(callback, index, fileCount, snapshot.pair.relativePath, progress.StageCompressingForward))
			if err != nil {
				return createdDifferential{}, createdDifferential{}, err
			}
			var reverse createdDifferential
			if options.CreateReverse {
				progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: fileCount, Path: snapshot.pair.relativePath, Stage: progress.StageCompressingReverse, TotalBytes: snapshot.source.Size})
				reverse, err = compressPreparedPayload(ctx, emptyReference, reverseRaw, filepath.Join(workDirectory, fmt.Sprintf("%06d.reverse.sparse.zst", index)), patchformat.MethodSparse, stats.expandedSize, options.CompressionLevel, compressionProgress(callback, index, fileCount, snapshot.pair.relativePath, progress.StageCompressingReverse))
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

	forward, err := createCopyAddOrReplace(ctx, snapshot.source.SnapshotPath, snapshot.target.SnapshotPath, snapshot.target.Size, workDirectory, emptyReference, index, "forward", options.CompressionLevel, compressionProgress(callback, index, fileCount, snapshot.pair.relativePath, progress.StageCompressingForward))
	if err != nil {
		return createdDifferential{}, createdDifferential{}, err
	}
	var reverse createdDifferential
	if options.CreateReverse {
		progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: fileCount, Path: snapshot.pair.relativePath, Stage: progress.StageCompressingReverse, TotalBytes: snapshot.source.Size})
		reverse, err = createCopyAddOrReplace(ctx, snapshot.target.SnapshotPath, snapshot.source.SnapshotPath, snapshot.source.Size, workDirectory, emptyReference, index, "reverse", options.CompressionLevel, compressionProgress(callback, index, fileCount, snapshot.pair.relativePath, progress.StageCompressingReverse))
		if err != nil {
			return createdDifferential{}, createdDifferential{}, err
		}
	}
	return forward, reverse, nil
}

func createCopyAddOrReplace(ctx context.Context, sourcePath, targetPath string, targetSize uint64, workDirectory, emptyReference string, index int, direction string, level int, callback zstd.ProgressFunc) (createdDifferential, error) {
	copyAddRaw := filepath.Join(workDirectory, fmt.Sprintf("%06d.%s.copy-add", index, direction))
	stats, usable, err := createCopyAddStreamOptimized(ctx, sourcePath, targetPath, copyAddRaw, targetSize)
	if err != nil {
		return createdDifferential{}, err
	}
	if usable {
		result, err := compressPreparedPayload(ctx, emptyReference, copyAddRaw, filepath.Join(workDirectory, fmt.Sprintf("%06d.%s.copy-add.zst", index, direction)), patchformat.MethodCopyAdd, stats.expandedSize, level, callback)
		_ = os.Remove(copyAddRaw)
		return result, err
	}
	return compressPreparedPayload(ctx, emptyReference, targetPath, filepath.Join(workDirectory, fmt.Sprintf("%06d.%s.replace.zst", index, direction)), patchformat.MethodReplace, 0, level, callback)
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

func compressPreparedPayload(ctx context.Context, referencePath, inputPath, outputPath, method string, expandedSize uint64, level int, callback zstd.ProgressFunc) (createdDifferential, error) {
	if err := ctx.Err(); err != nil {
		return createdDifferential{}, err
	}
	if err := zstd.CompressFile(referencePath, inputPath, outputPath, level, callback); err != nil {
		return createdDifferential{}, err
	}
	compressedSize, err := regularFileSize(outputPath)
	if err != nil {
		return createdDifferential{}, err
	}
	return createdDifferential{method: method, path: outputPath, compressedSize: compressedSize, expandedSize: expandedSize}, nil
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
		HashAlgorithm: hashutil.Algorithm,
		Compression: patchformat.Compression{
			Algorithm: patchformat.AlgorithmHybrid,
			Library:   zstd.Version(),
			Mode:      patchformat.CompressionHybridV2,
			Level:     options.CompressionLevel,
		},
		Reverse: options.CreateReverse,
		Files:   make([]patchformat.FileEntry, 0, capacity),
	}
}

func compressionProgress(callback progress.Callback, index, count int, path string, stage progress.Stage) zstd.ProgressFunc {
	return func(processed, total uint64) {
		progress.Report(callback, progress.Event{
			FileIndex:      index + 1,
			FileCount:      count,
			Path:           path,
			Stage:          stage,
			ProcessedBytes: processed,
			TotalBytes:     total,
		})
	}
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
