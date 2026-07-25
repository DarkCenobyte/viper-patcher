package patch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

type preparedFile struct {
	path      string
	temporary string
	backup    string
	mode      os.FileMode
	committed bool
}

// Apply validates and applies a patch direction transactionally.
func Apply(ctx context.Context, patchPath, root string, direction Direction, callback progress.Callback) error {
	parsed, err := Open(patchPath)
	if err != nil {
		return err
	}
	if direction == Reverse && !parsed.Header.Reverse {
		return fmt.Errorf("patch does not contain reverse differentials")
	}
	validation, err := Inspect(root, parsed)
	if err != nil {
		return err
	}
	if !validation.Ready(direction) {
		return validation.Error()
	}

	prepared := make([]preparedFile, 0, len(parsed.Header.Files))
	cleanupPrepared := func() {
		for _, file := range prepared {
			if !file.committed {
				os.Remove(file.temporary)
			}
		}
	}
	defer cleanupPrepared()

	for index, entry := range parsed.Header.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := pathutil.SecureJoinExisting(root, entry.Path)
		if err != nil {
			return err
		}
		directory := filepath.Dir(path)
		temporary, err := os.CreateTemp(directory, ".viper-patcher-output-*")
		if err != nil {
			return fmt.Errorf("create temporary output for %q: %w", entry.Path, err)
		}
		temporaryPath := temporary.Name()
		if err := temporary.Close(); err != nil {
			os.Remove(temporaryPath)
			return err
		}

		offset, length, expectedSize, expectedHash, mode := differential(entry, direction)
		progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: len(parsed.Header.Files), Path: entry.Path, Stage: "applying", TotalBytes: expectedSize})
		err = zstd.DecompressSegment(path, patchPath, parsed.DataOffset+offset, length, temporaryPath, expectedSize, func(processed, total uint64) {
			progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: len(parsed.Header.Files), Path: entry.Path, Stage: "applying", ProcessedBytes: processed, TotalBytes: total})
		})
		if err != nil {
			os.Remove(temporaryPath)
			return fmt.Errorf("apply %s differential for %q: %w", direction, entry.Path, err)
		}
		digest, size, err := hashutil.File(temporaryPath)
		if err != nil {
			os.Remove(temporaryPath)
			return err
		}
		if digest != expectedHash || size != expectedSize {
			os.Remove(temporaryPath)
			return fmt.Errorf("generated file %q failed integrity verification", entry.Path)
		}
		if err := os.Chmod(temporaryPath, mode); err != nil {
			os.Remove(temporaryPath)
			return fmt.Errorf("set permissions for %q: %w", entry.Path, err)
		}
		prepared = append(prepared, preparedFile{path: path, temporary: temporaryPath, mode: mode})
		progress.Report(callback, progress.Event{FileIndex: index + 1, FileCount: len(parsed.Header.Files), Path: entry.Path, Stage: "file-completed", ProcessedBytes: expectedSize, TotalBytes: expectedSize})
	}

	if err := commitPrepared(prepared); err != nil {
		return err
	}
	progress.Report(callback, progress.Event{FileIndex: len(parsed.Header.Files), FileCount: len(parsed.Header.Files), Stage: "completed"})
	return nil
}

func differential(entry patchformat.FileEntry, direction Direction) (offset, length, size uint64, hash string, mode os.FileMode) {
	if direction == Reverse {
		return entry.ReverseOffset, entry.ReverseLength, entry.SourceSize, entry.SourceHash, os.FileMode(entry.SourceMode)
	}
	return entry.ForwardOffset, entry.ForwardLength, entry.TargetSize, entry.TargetHash, os.FileMode(entry.TargetMode)
}

func commitPrepared(files []preparedFile) error {
	for index := range files {
		backupFile, err := os.CreateTemp(filepath.Dir(files[index].path), ".viper-patcher-backup-*")
		if err != nil {
			rollbackPrepared(files, index)
			return fmt.Errorf("reserve backup path: %w", err)
		}
		files[index].backup = backupFile.Name()
		backupFile.Close()
		os.Remove(files[index].backup)

		if err := os.Rename(files[index].path, files[index].backup); err != nil {
			rollbackPrepared(files, index)
			return fmt.Errorf("backup %q: %w", files[index].path, err)
		}
		if err := os.Rename(files[index].temporary, files[index].path); err != nil {
			_ = os.Rename(files[index].backup, files[index].path)
			rollbackPrepared(files, index)
			return fmt.Errorf("replace %q: %w", files[index].path, err)
		}
		files[index].committed = true
	}
	for index := range files {
		// The replacement is already committed. Backup cleanup is best-effort so a
		// transient antivirus or indexer lock cannot turn a successful patch into
		// a reported failure.
		_ = os.Remove(files[index].backup)
	}
	return nil
}

func rollbackPrepared(files []preparedFile, committedCount int) {
	for index := committedCount - 1; index >= 0; index-- {
		if files[index].committed {
			_ = os.Remove(files[index].path)
			_ = os.Rename(files[index].backup, files[index].path)
			files[index].committed = false
		}
	}
}
