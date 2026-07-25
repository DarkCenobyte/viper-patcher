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

func compressCreationInputs(ctx context.Context, options CreateOptions, snapshots []creationSnapshot, workDirectory string, callback progress.Callback) (patchformat.Header, []differentialBlobs, error) {
	header := newPatchHeader(options, len(snapshots))
	blobs := make([]differentialBlobs, len(snapshots))
	var dataOffset uint64

	for index, snapshot := range snapshots {
		if err := ctx.Err(); err != nil {
			return patchformat.Header{}, nil, err
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
			return patchformat.Header{}, nil, fmt.Errorf("create forward differential for %q: %w", snapshot.pair.relativePath, err)
		}
		forwardLength, err := regularFileSize(forwardPath)
		if err != nil {
			return patchformat.Header{}, nil, err
		}

		entry := patchformat.FileEntry{
			Path:          snapshot.pair.relativePath,
			TargetHint:    snapshot.pair.targetHint,
			SourceHash:    snapshot.source.Hash,
			TargetHash:    snapshot.target.Hash,
			SourceSize:    snapshot.source.Size,
			TargetSize:    snapshot.target.Size,
			SourceMode:    snapshot.source.Mode,
			TargetMode:    snapshot.target.Mode,
			ForwardOffset: dataOffset,
			ForwardLength: forwardLength,
		}
		blobs[index].forward = forwardPath
		dataOffset += forwardLength

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
				return patchformat.Header{}, nil, fmt.Errorf("create reverse differential for %q: %w", snapshot.pair.relativePath, err)
			}
			reverseLength, err := regularFileSize(reversePath)
			if err != nil {
				return patchformat.Header{}, nil, err
			}
			entry.ReverseOffset = dataOffset
			entry.ReverseLength = reverseLength
			blobs[index].reverse = reversePath
			dataOffset += reverseLength
		}
		header.Files = append(header.Files, entry)
		progress.Report(callback, progress.Event{
			FileIndex:      index + 1,
			FileCount:      len(snapshots),
			Path:           snapshot.pair.relativePath,
			Stage:          progress.StageFileCompleted,
			ProcessedBytes: 1,
			TotalBytes:     1,
		})
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
