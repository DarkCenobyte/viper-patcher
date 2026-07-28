package patch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
	"github.com/DarkCenobyte/viper-patcher/internal/workerbudget"
	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

type createPlan struct {
	pairs      []plannedPair
	outputPath string
}

type plannedPair struct {
	sourcePath   string
	targetPath   string
	relativePath string
	sourceSize   uint64
	targetSize   uint64
}

type resolvedInput struct {
	path string
	info os.FileInfo
}

func createPlanFromOptions(options CreateOptions) (createPlan, error) {
	if err := validateCreateOptions(options); err != nil {
		return createPlan{}, err
	}

	outputPath, err := filepath.Abs(options.OutputPath)
	if err != nil {
		return createPlan{}, fmt.Errorf("resolve output path: %w", err)
	}
	outputPath = filepath.Clean(outputPath)

	sourcePaths := make([]string, len(options.Files))
	resolvedSources := make([]resolvedInput, len(options.Files))
	resolvedTargets := make([]resolvedInput, len(options.Files))
	for index, pair := range options.Files {
		source, err := resolveRegularInput(pair.SourcePath)
		if err != nil {
			return createPlan{}, fmt.Errorf("source file %d: %w", index+1, err)
		}
		target, err := resolveRegularInput(pair.TargetPath)
		if err != nil {
			return createPlan{}, fmt.Errorf("target file %d: %w", index+1, err)
		}
		resolvedSources[index] = source
		resolvedTargets[index] = target
		sourcePaths[index] = source.path
	}
	if err := rejectOutputInputCollision(outputPath, resolvedSources, resolvedTargets); err != nil {
		return createPlan{}, err
	}

	sourceBase, err := pathutil.CommonBase(sourcePaths)
	if err != nil {
		return createPlan{}, fmt.Errorf("determine source base directory: %w", err)
	}
	plan := createPlan{pairs: make([]plannedPair, len(options.Files)), outputPath: outputPath}
	seenPaths := make(map[string]struct{}, len(options.Files))
	for index := range options.Files {
		relativePath, err := pathutil.RelativePatchPath(sourceBase, resolvedSources[index].path)
		if err != nil {
			return createPlan{}, err
		}
		if err := patchformat.ValidatePath(relativePath); err != nil {
			return createPlan{}, fmt.Errorf("source path %q cannot be stored in a portable patch: %w", relativePath, err)
		}
		pathKey := pathutil.CaseInsensitiveKey(relativePath)
		if _, exists := seenPaths[pathKey]; exists {
			return createPlan{}, fmt.Errorf("duplicate, Unicode-equivalent, or case-colliding source path %q", relativePath)
		}
		seenPaths[pathKey] = struct{}{}
		plan.pairs[index] = plannedPair{
			sourcePath:   resolvedSources[index].path,
			targetPath:   resolvedTargets[index].path,
			relativePath: relativePath,
			sourceSize:   uint64(resolvedSources[index].info.Size()),
			targetSize:   uint64(resolvedTargets[index].info.Size()),
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
	if !workerbudget.IsValid(options.WorkerBudget) {
		return fmt.Errorf("worker target must be 0 (automatic) or between 1 and %d", workerbudget.Maximum())
	}
	if strings.TrimSpace(options.WorkDirectory) != "" {
		if _, err := resolveWorkDirectory(options.WorkDirectory); err != nil {
			return err
		}
	}
	return nil
}

func effectiveWorkerBudget(value int) int {
	return workerbudget.Effective(value)
}

func resolveRegularInput(path string) (resolvedInput, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return resolvedInput{}, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return resolvedInput{}, err
	}
	opened, info, err := openStableRegularFile(resolved)
	if err != nil {
		return resolvedInput{}, err
	}
	if err := opened.Close(); err != nil {
		return resolvedInput{}, fmt.Errorf("close %q after validation: %w", path, err)
	}
	return resolvedInput{path: filepath.Clean(resolved), info: info}, nil
}

func rejectOutputInputCollision(outputPath string, sources, targets []resolvedInput) error {
	outputKey := pathutil.CaseInsensitiveKey(outputPath)
	var outputInfo os.FileInfo
	if info, err := os.Lstat(outputPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("existing output %q is not a regular file", outputPath)
		}
		outputInfo = info
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect existing output: %w", err)
	}

	for index := range sources {
		inputs := []struct {
			label string
			input resolvedInput
		}{
			{label: "source", input: sources[index]},
			{label: "target", input: targets[index]},
		}
		for _, candidate := range inputs {
			if pathutil.CaseInsensitiveKey(candidate.input.path) == outputKey ||
				(outputInfo != nil && os.SameFile(outputInfo, candidate.input.info)) {
				return fmt.Errorf("output path must not replace %s file %d (%q)", candidate.label, index+1, candidate.input.path)
			}
		}
	}
	return nil
}

func resolveWorkDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve work directory: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect work directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("work directory %q must be a regular directory, not a symbolic link", path)
	}
	return filepath.Clean(absolute), nil
}
