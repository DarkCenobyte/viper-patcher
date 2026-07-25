package patch

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

type createPlan struct {
	pairs []plannedPair
}

type plannedPair struct {
	sourcePath   string
	targetPath   string
	relativePath string
	targetHint   string
}

func createPlanFromOptions(options CreateOptions) (createPlan, error) {
	if err := validateCreateOptions(options); err != nil {
		return createPlan{}, err
	}

	sourcePaths := make([]string, len(options.Files))
	resolved := make([]FilePair, len(options.Files))
	for index, pair := range options.Files {
		sourcePath, err := absoluteRegularPath(pair.SourcePath)
		if err != nil {
			return createPlan{}, fmt.Errorf("source file %d: %w", index+1, err)
		}
		targetPath, err := absoluteRegularPath(pair.TargetPath)
		if err != nil {
			return createPlan{}, fmt.Errorf("target file %d: %w", index+1, err)
		}
		resolved[index] = FilePair{SourcePath: sourcePath, TargetPath: targetPath}
		sourcePaths[index] = sourcePath
	}

	sourceBase, err := pathutil.CommonBase(sourcePaths)
	if err != nil {
		return createPlan{}, fmt.Errorf("determine source base directory: %w", err)
	}
	plan := createPlan{pairs: make([]plannedPair, len(resolved))}
	seenPaths := make(map[string]struct{}, len(resolved))
	for index, pair := range resolved {
		relativePath, err := pathutil.RelativePatchPath(sourceBase, pair.SourcePath)
		if err != nil {
			return createPlan{}, err
		}
		pathKey := strings.ToLower(relativePath)
		if _, exists := seenPaths[pathKey]; exists {
			return createPlan{}, fmt.Errorf("duplicate or case-colliding source path %q", relativePath)
		}
		seenPaths[pathKey] = struct{}{}
		plan.pairs[index] = plannedPair{
			sourcePath:   pair.SourcePath,
			targetPath:   pair.TargetPath,
			relativePath: relativePath,
			targetHint:   filepath.Base(pair.TargetPath),
		}
	}
	return plan, nil
}

func validateCreateOptions(options CreateOptions) error {
	if len(options.Files) == 0 {
		return fmt.Errorf("at least one source/target file pair is required")
	}
	for index, pair := range options.Files {
		if strings.TrimSpace(pair.SourcePath) == "" || strings.TrimSpace(pair.TargetPath) == "" {
			return fmt.Errorf("file pair %d must contain both a source path and a target path", index+1)
		}
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

func absoluteRegularPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	opened, _, err := openStableRegularFile(info)
	if err != nil {
		return "", err
	}
	if err := opened.Close(); err != nil {
		return "", fmt.Errorf("close %q after validation: %w", path, err)
	}
	return filepath.Clean(info), nil
}
