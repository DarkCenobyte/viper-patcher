package patch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func TestCreateApplyAndReverse(t *testing.T) {
	workspace := t.TempDir()
	sourceRoot := filepath.Join(workspace, "old")
	targetRoot := filepath.Join(workspace, "new")
	applyRoot := filepath.Join(workspace, "installed")
	oldGame := []byte(strings.Repeat("OLD-GAME-", 10000))
	newGame := append([]byte("NEW-HEADER"), oldGame...)
	oldAssets := []byte(strings.Repeat("asset-v1-", 8000))
	newAssets := []byte(strings.Repeat("asset-v2-", 8000))

	sourceGame := filepath.Join(sourceRoot, "bin", "game.exe")
	sourceAssets := filepath.Join(sourceRoot, "data", "assets.bin")
	targetGame := filepath.Join(targetRoot, "renamed", "different-name.exe")
	targetAssets := filepath.Join(targetRoot, "assets-new.bin")
	writeFile(t, sourceGame, oldGame)
	writeFile(t, sourceAssets, oldAssets)
	writeFile(t, targetGame, newGame)
	writeFile(t, targetAssets, newAssets)
	writeFile(t, filepath.Join(applyRoot, "bin", "game.exe"), oldGame)
	writeFile(t, filepath.Join(applyRoot, "data", "assets.bin"), oldAssets)

	patchPath := filepath.Join(workspace, "update.vipr")
	createPatch(t, patchPath, []FilePair{
		{SourcePath: sourceGame, TargetPath: targetGame},
		{SourcePath: sourceAssets, TargetPath: targetAssets},
	}, true, "Install version 2.", nil)

	parsed := mustOpen(t, patchPath)
	if parsed.Header.Comment != "Install version 2." || !parsed.Header.Reverse || len(parsed.Header.Files) != 2 {
		t.Fatalf("unexpected header: %#v", parsed.Header)
	}
	if parsed.Header.Files[0].Path != "bin/game.exe" || parsed.Header.Files[1].Path != "data/assets.bin" {
		t.Fatalf("unexpected paths: %#v", parsed.Header.Files)
	}

	validation, err := Inspect(applyRoot, parsed)
	if err != nil || validation.State != StateForwardReady || !validation.CanApplyForward || validation.CanApplyReverse {
		t.Fatalf("forward validation = %#v, %v", validation, err)
	}
	completed := make(map[string]bool)
	if err := Apply(context.Background(), patchPath, applyRoot, Forward, func(event progress.Event) {
		if event.Stage == progress.StageFileCompleted {
			completed[event.Path] = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(completed) != 2 {
		t.Fatalf("completed progress events = %#v", completed)
	}
	assertFile(t, filepath.Join(applyRoot, "bin", "game.exe"), newGame)
	assertFile(t, filepath.Join(applyRoot, "data", "assets.bin"), newAssets)

	validation, err = Inspect(applyRoot, parsed)
	if err != nil || validation.State != StateReverseReady || validation.CanApplyForward || !validation.CanApplyReverse {
		t.Fatalf("reverse validation = %#v, %v", validation, err)
	}
	if err := Apply(context.Background(), patchPath, applyRoot, Reverse, nil); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(applyRoot, "bin", "game.exe"), oldGame)
	assertFile(t, filepath.Join(applyRoot, "data", "assets.bin"), oldAssets)
}

func TestCreateReplacesExistingPatch(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "source.bin")
	target := filepath.Join(workspace, "target.bin")
	output := filepath.Join(workspace, "update.vipr")
	writeFile(t, source, []byte("source"))
	writeFile(t, target, []byte("target"))
	writeFile(t, output, []byte("old patch"))

	createPatch(t, output, []FilePair{{SourcePath: source, TargetPath: target}}, false, "replacement", nil)
	parsed := mustOpen(t, output)
	if parsed.Header.Comment != "replacement" {
		t.Fatalf("comment = %q", parsed.Header.Comment)
	}
}

func TestCreateRejectsIncompletePair(t *testing.T) {
	err := Create(context.Background(), CreateOptions{
		Files:            []FilePair{{SourcePath: "source.bin"}},
		OutputPath:       "update.vipr",
		CompressionLevel: 3,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "both a source path and a target path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateRejectsDuplicateSourcePaths(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "old", "application.bin")
	targetOne := filepath.Join(workspace, "new", "application-one.bin")
	targetTwo := filepath.Join(workspace, "new", "application-two.bin")
	writeFile(t, source, []byte("source"))
	writeFile(t, targetOne, []byte("target-one"))
	writeFile(t, targetTwo, []byte("target-two"))

	err := Create(context.Background(), CreateOptions{
		Files: []FilePair{
			{SourcePath: source, TargetPath: targetOne},
			{SourcePath: source, TargetPath: targetTwo},
		},
		OutputPath:       filepath.Join(workspace, "update.vipr"),
		CompressionLevel: 3,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate or case-colliding source path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateUsesImmutableSnapshots(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "old", "application.bin")
	target := filepath.Join(workspace, "new", "application.bin")
	installed := filepath.Join(workspace, "installed", "application.bin")
	patchPath := filepath.Join(workspace, "update.vipr")
	originalSource := []byte(strings.Repeat("source-before-", 4096))
	originalTarget := []byte(strings.Repeat("target-before-", 4096))
	writeFile(t, source, originalSource)
	writeFile(t, target, originalTarget)
	writeFile(t, installed, originalSource)

	var once sync.Once
	createPatch(t, patchPath, []FilePair{{SourcePath: source, TargetPath: target}}, false, "", func(event progress.Event) {
		if event.Stage == progress.StageCompressingForward {
			once.Do(func() {
				if err := os.WriteFile(source, []byte("source-after"), 0o644); err != nil {
					t.Errorf("replace source during create: %v", err)
				}
				if err := os.WriteFile(target, []byte("target-after"), 0o644); err != nil {
					t.Errorf("replace target during create: %v", err)
				}
			})
		}
	})

	if err := Apply(context.Background(), patchPath, filepath.Dir(installed), Forward, nil); err != nil {
		t.Fatal(err)
	}
	assertFile(t, installed, originalTarget)
}

func TestApplyUsesImmutablePatchSnapshot(t *testing.T) {
	fixture := newSingleFileFixture(t, true)
	var once sync.Once
	err := Apply(context.Background(), fixture.patchPath, fixture.installRoot, Forward, func(event progress.Event) {
		if event.Stage == progress.StagePreparing {
			once.Do(func() {
				if err := os.WriteFile(fixture.patchPath, []byte("corrupted after snapshot"), 0o644); err != nil {
					t.Errorf("replace patch during apply: %v", err)
				}
			})
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, fixture.installedPath, fixture.targetData)
}

func TestApplyRejectsPatchChangedAfterInspection(t *testing.T) {
	fixture := newSingleFileFixture(t, false)
	_, digest, err := OpenWithDigest(fixture.patchPath)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(fixture.patchPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("changed")); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	err = ApplyWithOptions(context.Background(), ApplyOptions{
		PatchPath:         fixture.patchPath,
		Root:              fixture.installRoot,
		Direction:         Forward,
		ExpectedPatchHash: digest,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "selected patch changed") {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFile(t, fixture.installedPath, fixture.sourceData)
}

func TestApplyRejectsInstalledFileChangedBeforeCommit(t *testing.T) {
	fixture := newSingleFileFixture(t, false)
	externalContent := []byte("changed-by-another-process")
	var once sync.Once
	err := Apply(context.Background(), fixture.patchPath, fixture.installRoot, Forward, func(event progress.Event) {
		if event.Stage == progress.StageFileCompleted {
			once.Do(func() {
				if writeErr := os.WriteFile(fixture.installedPath, externalContent, 0o644); writeErr != nil {
					t.Errorf("change installed file: %v", writeErr)
				}
			})
		}
	})
	if err == nil || !strings.Contains(err.Error(), "changed after validation") {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFile(t, fixture.installedPath, externalContent)
}

func TestInspectMixedState(t *testing.T) {
	workspace := t.TempDir()
	sourceRoot := filepath.Join(workspace, "old")
	targetRoot := filepath.Join(workspace, "new")
	installRoot := filepath.Join(workspace, "install")
	pairs := make([]FilePair, 0, 2)
	for _, name := range []string{"one.bin", "two.bin"} {
		source := filepath.Join(sourceRoot, name)
		target := filepath.Join(targetRoot, name)
		writeFile(t, source, []byte("old-"+name))
		writeFile(t, target, []byte("new-"+name))
		pairs = append(pairs, FilePair{SourcePath: source, TargetPath: target})
	}
	writeFile(t, filepath.Join(installRoot, "one.bin"), []byte("old-one.bin"))
	writeFile(t, filepath.Join(installRoot, "two.bin"), []byte("new-two.bin"))
	patchPath := filepath.Join(workspace, "mixed.vipr")
	createPatch(t, patchPath, pairs, true, "", nil)

	result, err := Inspect(installRoot, mustOpen(t, patchPath))
	if err != nil || result.State != StateMixedFiles || len(result.Issues) != 0 {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestInspectReportsMissingHashAndModeIssues(t *testing.T) {
	fixture := newSingleFileFixture(t, true)
	if err := os.Remove(fixture.installedPath); err != nil {
		t.Fatal(err)
	}
	parsed := mustOpen(t, fixture.patchPath)
	result, err := Inspect(fixture.installRoot, parsed)
	if err != nil || result.State != StateMissingFiles || len(result.Missing) != 1 {
		t.Fatalf("missing result = %#v, %v", result, err)
	}

	writeFile(t, fixture.installedPath, []byte("unknown"))
	result, err = Inspect(fixture.installRoot, parsed)
	if err != nil || result.State != StateInvalidFiles || len(result.Issues) != 1 || result.Issues[0].Reason != IssueHashMismatch {
		t.Fatalf("hash result = %#v, %v", result, err)
	}

	if runtime.GOOS != "windows" {
		writeFileMode(t, fixture.installedPath, fixture.sourceData, 0o600)
		result, err = Inspect(fixture.installRoot, parsed)
		if err != nil || result.State != StateInvalidFiles || len(result.Issues) != 1 || result.Issues[0].Reason != IssueModeMismatch {
			t.Fatalf("mode result = %#v, %v", result, err)
		}
	}
}

func TestInspectSameSourceAndTargetState(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "old", "same.bin")
	target := filepath.Join(workspace, "new", "same.bin")
	installed := filepath.Join(workspace, "install", "same.bin")
	content := []byte(strings.Repeat("same-content", 512))
	writeFile(t, source, content)
	writeFile(t, target, content)
	writeFile(t, installed, content)
	patchPath := filepath.Join(workspace, "same.vipr")
	createPatch(t, patchPath, []FilePair{{SourcePath: source, TargetPath: target}}, true, "", nil)

	result, err := Inspect(filepath.Dir(installed), mustOpen(t, patchPath))
	if err != nil || result.State != StateBidirectionalReady || !result.CanApplyForward || !result.CanApplyReverse {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestInspectSameHashDifferentModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission mode semantics differ on Windows")
	}
	workspace := t.TempDir()
	source := filepath.Join(workspace, "old", "same.bin")
	target := filepath.Join(workspace, "new", "same.bin")
	installed := filepath.Join(workspace, "install", "same.bin")
	content := []byte("same-content")
	writeFileMode(t, source, content, 0o600)
	writeFileMode(t, target, content, 0o755)
	writeFileMode(t, installed, content, 0o600)
	patchPath := filepath.Join(workspace, "mode.vipr")
	createPatch(t, patchPath, []FilePair{{SourcePath: source, TargetPath: target}}, true, "", nil)

	result, err := Inspect(filepath.Dir(installed), mustOpen(t, patchPath))
	if err != nil || result.State != StateForwardReady || !result.CanApplyForward || result.CanApplyReverse {
		t.Fatalf("source-mode result = %#v, err = %v", result, err)
	}
	if err := os.Chmod(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err = Inspect(filepath.Dir(installed), mustOpen(t, patchPath))
	if err != nil || result.State != StateReverseReady || result.CanApplyForward || !result.CanApplyReverse {
		t.Fatalf("target-mode result = %#v, err = %v", result, err)
	}
}

func TestSetPortableModeMasksSpecialBits(t *testing.T) {
	var actual os.FileMode
	err := setPortableMode("unused", 0o4755, func(_ string, mode os.FileMode) error {
		actual = mode
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if actual != 0o755 {
		t.Fatalf("mode = %#o, want 0755", actual)
	}
}

func TestApplyReportsChmodFailure(t *testing.T) {
	fixture := newSingleFileFixture(t, false)
	expected := errors.New("chmod failed")
	err := applyWithOperations(context.Background(), ApplyOptions{PatchPath: fixture.patchPath, Root: fixture.installRoot, Direction: Forward}, nil, applyOperations{
		chmod: func(string, os.FileMode) error { return expected },
	})
	if !errors.Is(err, expected) {
		t.Fatalf("error = %v", err)
	}
	assertFile(t, fixture.installedPath, fixture.sourceData)
}

func TestTransactionRollsBackCompletedReplacements(t *testing.T) {
	workspace := t.TempDir()
	targetOne := filepath.Join(workspace, "one.bin")
	targetTwo := filepath.Join(workspace, "two.bin")
	temporaryOne := filepath.Join(workspace, "one.new")
	temporaryTwo := filepath.Join(workspace, "two.new")
	writeFile(t, targetOne, []byte("old-one"))
	writeFile(t, targetTwo, []byte("old-two"))
	writeFile(t, temporaryOne, []byte("new-one"))
	writeFile(t, temporaryTwo, []byte("new-two"))

	renameCalls := 0
	operations := defaultTransactionOperations
	operations.rename = func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 4 {
			return fmt.Errorf("injected replacement failure")
		}
		return os.Rename(oldPath, newPath)
	}
	transaction := newTransactionWithOperations(operations)
	if err := transaction.Add(targetOne, temporaryOne, expectationFor(t, targetOne)); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Add(targetTwo, temporaryTwo, expectationFor(t, targetTwo)); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err == nil || !strings.Contains(err.Error(), "injected replacement failure") {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFile(t, targetOne, []byte("old-one"))
	assertFile(t, targetTwo, []byte("old-two"))
	assertPathMissing(t, temporaryOne)
	assertPathMissing(t, temporaryTwo)
	backups, err := filepath.Glob(filepath.Join(workspace, ".viper-patcher-backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("unexpected backup files: %v", backups)
	}
}

func TestTransactionIncludesRollbackFailure(t *testing.T) {
	workspace := t.TempDir()
	targetOne := filepath.Join(workspace, "one.bin")
	targetTwo := filepath.Join(workspace, "two.bin")
	temporaryOne := filepath.Join(workspace, "one.new")
	temporaryTwo := filepath.Join(workspace, "two.new")
	writeFile(t, targetOne, []byte("old-one"))
	writeFile(t, targetTwo, []byte("old-two"))
	writeFile(t, temporaryOne, []byte("new-one"))
	writeFile(t, temporaryTwo, []byte("new-two"))

	renameCalls := 0
	operations := defaultTransactionOperations
	operations.rename = func(oldPath, newPath string) error {
		renameCalls++
		switch renameCalls {
		case 4:
			return fmt.Errorf("injected replacement failure")
		case 5:
			return fmt.Errorf("injected restore failure")
		default:
			return os.Rename(oldPath, newPath)
		}
	}
	transaction := newTransactionWithOperations(operations)
	if err := transaction.Add(targetOne, temporaryOne, expectationFor(t, targetOne)); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Add(targetTwo, temporaryTwo, expectationFor(t, targetTwo)); err != nil {
		t.Fatal(err)
	}
	err := transaction.Commit()
	if err == nil || !strings.Contains(err.Error(), "rollback failed") || !strings.Contains(err.Error(), "injected restore failure") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTransactionIncludesRollbackRemoveFailure(t *testing.T) {
	workspace := t.TempDir()
	targetOne := filepath.Join(workspace, "one.bin")
	targetTwo := filepath.Join(workspace, "two.bin")
	temporaryOne := filepath.Join(workspace, "one.new")
	temporaryTwo := filepath.Join(workspace, "two.new")
	writeFile(t, targetOne, []byte("old-one"))
	writeFile(t, targetTwo, []byte("old-two"))
	writeFile(t, temporaryOne, []byte("new-one"))
	writeFile(t, temporaryTwo, []byte("new-two"))

	renameCalls := 0
	expected := errors.New("injected rollback remove failure")
	operations := defaultTransactionOperations
	operations.rename = func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 4 {
			return fmt.Errorf("injected replacement failure")
		}
		return os.Rename(oldPath, newPath)
	}
	operations.remove = func(path string) error {
		if path == targetOne && renameCalls >= 4 {
			return expected
		}
		return os.Remove(path)
	}
	transaction := newTransactionWithOperations(operations)
	if err := transaction.Add(targetOne, temporaryOne, expectationFor(t, targetOne)); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Add(targetTwo, temporaryTwo, expectationFor(t, targetTwo)); err != nil {
		t.Fatal(err)
	}
	err := transaction.Commit()
	if !errors.Is(err, expected) || !strings.Contains(err.Error(), "remove replacement") {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFile(t, targetTwo, []byte("old-two"))
}

func TestTransactionIncludesCleanupFailureAfterCommitError(t *testing.T) {
	workspace := t.TempDir()
	targetOne := filepath.Join(workspace, "one.bin")
	targetTwo := filepath.Join(workspace, "two.bin")
	temporaryOne := filepath.Join(workspace, "one.new")
	temporaryTwo := filepath.Join(workspace, "two.new")
	writeFile(t, targetOne, []byte("old-one"))
	writeFile(t, targetTwo, []byte("old-two"))
	writeFile(t, temporaryOne, []byte("new-one"))
	writeFile(t, temporaryTwo, []byte("new-two"))

	renameCalls := 0
	expected := errors.New("injected cleanup failure")
	operations := defaultTransactionOperations
	operations.rename = func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 4 {
			return fmt.Errorf("injected replacement failure")
		}
		return os.Rename(oldPath, newPath)
	}
	operations.remove = func(path string) error {
		if path == temporaryTwo && renameCalls >= 4 {
			return expected
		}
		return os.Remove(path)
	}
	transaction := newTransactionWithOperations(operations)
	if err := transaction.Add(targetOne, temporaryOne, expectationFor(t, targetOne)); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Add(targetTwo, temporaryTwo, expectationFor(t, targetTwo)); err != nil {
		t.Fatal(err)
	}
	err := transaction.Commit()
	if !errors.Is(err, expected) || !strings.Contains(err.Error(), "cleanup after failed transaction") {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFile(t, targetOne, []byte("old-one"))
	assertFile(t, targetTwo, []byte("old-two"))
}

func TestTransactionIncludesCleanupFailure(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "target.bin")
	temporary := filepath.Join(workspace, "temporary.bin")
	writeFile(t, target, []byte("old"))
	writeFile(t, temporary, []byte("new"))
	expected := errors.New("remove failed")
	operations := defaultTransactionOperations
	operations.remove = func(path string) error {
		if path == temporary {
			return expected
		}
		return os.Remove(path)
	}
	transaction := newTransactionWithOperations(operations)
	if err := transaction.Add(target, temporary, expectationFor(t, target)); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Cleanup(); !errors.Is(err, expected) {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenRejectsTrailingData(t *testing.T) {
	fixture := newSingleFileFixture(t, false)
	file, err := os.OpenFile(fixture.patchPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("hidden")); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(fixture.patchPath); err == nil {
		t.Fatal("expected trailing data to be rejected")
	}
}

func TestOpenRejectsSymbolicLinkPatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic link creation generally requires additional privileges on Windows")
	}
	fixture := newSingleFileFixture(t, false)
	linkPath := filepath.Join(t.TempDir(), "linked.vipr")
	if err := os.Symlink(fixture.patchPath, linkPath); err != nil {
		t.Skipf("cannot create symbolic link: %v", err)
	}
	if _, err := Open(linkPath); err == nil {
		t.Fatal("expected symbolic-link patch to be rejected")
	}
}

func TestInspectRejectsSymbolicLinkTraversal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic link creation generally requires additional privileges on Windows")
	}
	fixture := newSingleFileFixture(t, false)
	outside := filepath.Join(filepath.Dir(fixture.installRoot), "outside")
	writeFile(t, filepath.Join(outside, "application.bin"), fixture.sourceData)
	if err := os.RemoveAll(fixture.installRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, fixture.installRoot); err != nil {
		t.Skipf("cannot create symbolic link: %v", err)
	}
	if _, err := Inspect(fixture.installRoot, mustOpen(t, fixture.patchPath)); err == nil {
		t.Fatal("expected symbolic-link traversal to be rejected")
	}
}

func TestApplyRejectsUnavailableDirectionAndCancellation(t *testing.T) {
	fixture := newSingleFileFixture(t, false)
	if err := Apply(context.Background(), fixture.patchPath, fixture.installRoot, Reverse, nil); err == nil {
		t.Fatal("expected reverse application without reverse data to fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Apply(ctx, fixture.patchPath, fixture.installRoot, Forward, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestApplyRejectsHashMismatch(t *testing.T) {
	fixture := newSingleFileFixture(t, false)
	writeFile(t, fixture.installedPath, []byte("unknown"))
	if err := Apply(context.Background(), fixture.patchPath, fixture.installRoot, Forward, nil); err == nil {
		t.Fatal("expected hash mismatch to fail")
	}
}

func FuzzOpen(f *testing.F) {
	f.Add([]byte("not a patch"))
	f.Add(append(append([]byte(nil), patchformat.Magic[:]...), make([]byte, 8)...))
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "fuzz.vipr")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = Open(path)
	})
}

type singleFileFixture struct {
	patchPath     string
	installRoot   string
	installedPath string
	sourceData    []byte
	targetData    []byte
}

func newSingleFileFixture(t *testing.T, reverse bool) singleFileFixture {
	t.Helper()
	workspace := t.TempDir()
	source := filepath.Join(workspace, "source", "application.bin")
	target := filepath.Join(workspace, "target", "renamed.bin")
	installRoot := filepath.Join(workspace, "install")
	installed := filepath.Join(installRoot, "application.bin")
	patchPath := filepath.Join(workspace, "update.vipr")
	sourceData := []byte(strings.Repeat("source-data-", 2048))
	targetData := []byte(strings.Repeat("target-data-", 2048))
	writeFile(t, source, sourceData)
	writeFile(t, target, targetData)
	writeFile(t, installed, sourceData)
	createPatch(t, patchPath, []FilePair{{SourcePath: source, TargetPath: target}}, reverse, "", nil)
	return singleFileFixture{
		patchPath:     patchPath,
		installRoot:   installRoot,
		installedPath: installed,
		sourceData:    sourceData,
		targetData:    targetData,
	}
}

func createPatch(t *testing.T, outputPath string, pairs []FilePair, reverse bool, comment string, callback progress.Callback) {
	t.Helper()
	if err := Create(context.Background(), CreateOptions{
		Files:            pairs,
		OutputPath:       outputPath,
		CompressionLevel: 3,
		Comment:          comment,
		CreateReverse:    reverse,
	}, callback); err != nil {
		t.Fatal(err)
	}
}

func mustOpen(t *testing.T, path string) patchformat.Patch {
	t.Helper()
	parsed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func expectationFor(t *testing.T, path string) fileExpectation {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	digest, size, err := hashutil.File(path)
	if err != nil {
		t.Fatal(err)
	}
	return fileExpectation{Identity: info, Hash: digest, Size: size, Mode: uint32(info.Mode().Perm())}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	writeFileMode(t, path, data, 0o644)
}

func writeFileMode(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be missing, got %v", path, err)
	}
}

func assertFile(t *testing.T, path string, expected []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("%s content mismatch", path)
	}
}
