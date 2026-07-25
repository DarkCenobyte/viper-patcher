package patch

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DarkCenobyte/viper-patcher/internal/buildinfo"
	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

// CreateOptions configures a VIPR patch creation operation.
type CreateOptions struct {
	SourceFiles      []string
	TargetFiles      []string
	OutputPath       string
	CompressionLevel int
	Comment          string
	CreateReverse    bool
}

// Create generates a VIPR patch atomically.
func Create(ctx context.Context, options CreateOptions, callback progress.Callback) error {
	if err := validateCreateOptions(options); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	sourceBase, err := pathutil.CommonBase(options.SourceFiles)
	if err != nil {
		return fmt.Errorf("determine source base directory: %w", err)
	}

	workDirectory, err := os.MkdirTemp("", "viper-patcher-create-*")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(workDirectory)

	header := patchformat.Header{
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
		Files:   make([]patchformat.FileEntry, 0, len(options.SourceFiles)),
	}

	type blobs struct {
		forward string
		reverse string
	}
	blobFiles := make([]blobs, len(options.SourceFiles))
	var dataOffset uint64

	for index := range options.SourceFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourcePath, err := absoluteRegularFile(options.SourceFiles[index])
		if err != nil {
			return fmt.Errorf("source file %d: %w", index+1, err)
		}
		targetPath, err := absoluteRegularFile(options.TargetFiles[index])
		if err != nil {
			return fmt.Errorf("target file %d: %w", index+1, err)
		}
		relativePath, err := pathutil.RelativePatchPath(sourceBase, sourcePath)
		if err != nil {
			return err
		}

		progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: len(options.SourceFiles), Path: relativePath, Stage: "hashing"})
		sourceHash, sourceSize, err := hashutil.File(sourcePath)
		if err != nil {
			return err
		}
		targetHash, targetSize, err := hashutil.File(targetPath)
		if err != nil {
			return err
		}
		sourceInfo, _ := os.Stat(sourcePath)
		targetInfo, _ := os.Stat(targetPath)

		forwardPath := filepath.Join(workDirectory, fmt.Sprintf("%06d.forward.zst", index))
		progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: len(options.SourceFiles), Path: relativePath, Stage: "compressing-forward", TotalBytes: targetSize})
		err = zstd.CompressFile(sourcePath, targetPath, forwardPath, options.CompressionLevel, func(processed, total uint64) {
			progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: len(options.SourceFiles), Path: relativePath, Stage: "compressing-forward", ProcessedBytes: processed, TotalBytes: total})
		})
		if err != nil {
			return fmt.Errorf("create forward differential for %q: %w", relativePath, err)
		}
		forwardInfo, err := os.Stat(forwardPath)
		if err != nil {
			return err
		}

		entry := patchformat.FileEntry{
			Path:          relativePath,
			TargetHint:    filepath.Base(targetPath),
			SourceHash:    sourceHash,
			TargetHash:    targetHash,
			SourceSize:    sourceSize,
			TargetSize:    targetSize,
			SourceMode:    uint32(sourceInfo.Mode().Perm()),
			TargetMode:    uint32(targetInfo.Mode().Perm()),
			ForwardOffset: dataOffset,
			ForwardLength: uint64(forwardInfo.Size()),
		}
		blobFiles[index].forward = forwardPath
		dataOffset += entry.ForwardLength

		if options.CreateReverse {
			reversePath := filepath.Join(workDirectory, fmt.Sprintf("%06d.reverse.zst", index))
			progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: len(options.SourceFiles), Path: relativePath, Stage: "compressing-reverse", TotalBytes: sourceSize})
			err = zstd.CompressFile(targetPath, sourcePath, reversePath, options.CompressionLevel, func(processed, total uint64) {
				progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: len(options.SourceFiles), Path: relativePath, Stage: "compressing-reverse", ProcessedBytes: processed, TotalBytes: total})
			})
			if err != nil {
				return fmt.Errorf("create reverse differential for %q: %w", relativePath, err)
			}
			reverseInfo, err := os.Stat(reversePath)
			if err != nil {
				return err
			}
			entry.ReverseOffset = dataOffset
			entry.ReverseLength = uint64(reverseInfo.Size())
			blobFiles[index].reverse = reversePath
			dataOffset += entry.ReverseLength
		}
		header.Files = append(header.Files, entry)
		progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: len(options.SourceFiles), Path: relativePath, Stage: "file-completed", ProcessedBytes: 1, TotalBytes: 1})
	}

	outputDirectory := filepath.Dir(options.OutputPath)
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temporaryOutput, err := os.CreateTemp(outputDirectory, ".viper-patcher-*.vipr.tmp")
	if err != nil {
		return fmt.Errorf("create temporary patch: %w", err)
	}
	temporaryPath := temporaryOutput.Name()
	committed := false
	defer func() {
		temporaryOutput.Close()
		if !committed {
			os.Remove(temporaryPath)
		}
	}()

	if _, err := patchformat.EncodePrefix(temporaryOutput, header); err != nil {
		return err
	}
	for _, blob := range blobFiles {
		if err := appendFile(temporaryOutput, blob.forward); err != nil {
			return err
		}
		if blob.reverse != "" {
			if err := appendFile(temporaryOutput, blob.reverse); err != nil {
				return err
			}
		}
	}
	if err := temporaryOutput.Sync(); err != nil {
		return fmt.Errorf("sync patch file: %w", err)
	}
	if err := temporaryOutput.Close(); err != nil {
		return fmt.Errorf("close patch file: %w", err)
	}
	if err := replaceOutput(temporaryPath, options.OutputPath); err != nil {
		return err
	}
	committed = true
	progress.Report(callback, progress.Event{FileIndex: len(options.SourceFiles), FileCount: len(options.SourceFiles), Stage: "completed"})
	return nil
}

func validateCreateOptions(options CreateOptions) error {
	if len(options.SourceFiles) == 0 || len(options.TargetFiles) == 0 {
		return fmt.Errorf("source-files and target-files must not be empty")
	}
	if len(options.SourceFiles) != len(options.TargetFiles) {
		return fmt.Errorf("source-files and target-files must contain exactly the same number of files")
	}
	if strings.TrimSpace(options.OutputPath) == "" {
		return fmt.Errorf("output path is required")
	}
	if !strings.EqualFold(filepath.Ext(options.OutputPath), ".vipr") {
		return fmt.Errorf("output path must use the .vipr extension")
	}
	minimum, maximum := zstd.CompressionLevelRange()
	if options.CompressionLevel < minimum || options.CompressionLevel > maximum {
		return fmt.Errorf("compression level must be between %d and %d", minimum, maximum)
	}
	return nil
}

func absoluteRegularFile(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", path)
	}
	return filepath.Clean(absolute), nil
}

func appendFile(output io.Writer, path string) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("append %q: %w", path, err)
	}
	return nil
}

func replaceOutput(source, destination string) error {
	_, err := os.Stat(destination)
	if os.IsNotExist(err) {
		if err := os.Rename(source, destination); err != nil {
			return fmt.Errorf("commit patch output: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing output: %w", err)
	}

	backupFile, err := os.CreateTemp(filepath.Dir(destination), ".viper-patcher-patch-backup-*")
	if err != nil {
		return fmt.Errorf("reserve patch backup path: %w", err)
	}
	backupPath := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		os.Remove(backupPath)
		return fmt.Errorf("close patch backup placeholder: %w", err)
	}
	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("release patch backup path: %w", err)
	}
	if err := os.Rename(destination, backupPath); err != nil {
		return fmt.Errorf("backup existing patch: %w", err)
	}
	if err := os.Rename(source, destination); err != nil {
		_ = os.Rename(backupPath, destination)
		return fmt.Errorf("commit patch output: %w", err)
	}
	_ = os.Remove(backupPath)
	return nil
}
