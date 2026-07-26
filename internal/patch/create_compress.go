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

func compressCreationInputs(ctx context.Context, options CreateOptions, snapshots []creationSnapshot, workDirectory string, parallelism int, callback progress.Callback) (patchformat.Header, []differentialBlobs, error) {
	compressed := make([]compressedCreationFile, len(snapshots))
	err := parallelFor(ctx, len(snapshots), parallelism, func(ctx context.Context, index int) error {
		snapshot := snapshots[index]
		if err := ctx.Err(); err != nil {
			return err
		}
		forwardPath := filepath.Join(workDirectory, fmt.Sprintf("%06d.forward.zst", index))
		progress.Report(callback, progress.Event{
			FileIndex:  index + 1,
			FileCount:  len(snapshots),
			Path:       snapshot.pair.relativePath,
			Stage:      progress.StageCompressingForward,
			TotalBytes: snapshot.target.Size,
		})
		if err := zstd.CompressFile(snapshot.source.SnapshotPath, snapshot.target.SnapshotPath, forwardPath, options.CompressionLevel, compressionProgress(callback, index, len(snapshots), snapshot.pair.relativePath, progress.StageCompressingForward)); err != nil {
			return fmt.Errorf("create forward differential for %q: %w", snapshot.pair.relativePath, err)
		}
		forwardLength, err := regularFileSize(forwardPath)
		if err != nil {
			return err
		}

		result := compressedCreationFile{
			entry: patchformat.FileEntry{
				Path:          snapshot.pair.relativePath,
				SourceHash:    snapshot.source.Hash,
				TargetHash:    snapshot.target.Hash,
				SourceSize:    snapshot.source.Size,
				TargetSize:    snapshot.target.Size,
				SourceMode:    snapshot.source.Mode,
				TargetMode:    snapshot.target.Mode,
				ForwardLength: forwardLength,
			},
			blobs: differentialBlobs{forward: forwardPath},
		}

		if options.CreateReverse {
			reversePath := filepath.Join(workDirectory, fmt.Sprintf("%06d.reverse.zst", index))
			progress.Report(callback, progress.Event{
				FileIndex:  index + 1,
				FileCount:  len(snapshots),
				Path:       snapshot.pair.relativePath,
				Stage:      progress.StageCompressingReverse,
				TotalBytes: snapshot.source.Size,
			})
			if err := zstd.CompressFile(snapshot.target.SnapshotPath, snapshot.source.SnapshotPath, reversePath, options.CompressionLevel, compressionProgress(callback, index, len(snapshots), snapshot.pair.relativePath, progress.StageCompressingReverse)); err != nil {
				return fmt.Errorf("create reverse differential for %q: %w", snapshot.pair.relativePath, err)
			}
			reverseLength, err := regularFileSize(reversePath)
			if err != nil {
				return err
			}
			result.entry.ReverseLength = reverseLength
			result.blobs.reverse = reversePath
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
			Algorithm: "zstd",
			Library:   zstd.Version(),
			Mode:      "patch-from",
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
