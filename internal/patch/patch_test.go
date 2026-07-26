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
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
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
		if event.Stage == progress.StageFilePrepared {
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

func TestInspectReportsMissingAndHashIssues(t *testing.T) {
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

func TestInspectIgnoresPermissionDifferences(t *testing.T) {
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
	writeFileMode(t, installed, content, 0o400)
	patchPath := filepath.Join(workspace, "mode.vipr")
	createPatch(t, patchPath, []FilePair{{SourcePath: source, TargetPath: target}}, true, "", nil)

	result, err := Inspect(filepath.Dir(installed), mustOpen(t, patchPath))
	if err != nil || result.State != StateBidirectionalReady || !result.CanApplyForward || !result.CanApplyReverse {
		t.Fatalf("permission-independent result = %#v, err = %v", result, err)
	}
}

func TestApplyPreservesInstalledPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission mode semantics differ on Windows")
	}
	workspace := t.TempDir()
	source := filepath.Join(workspace, "old", "application.bin")
	target := filepath.Join(workspace, "new", "application.bin")
	installed := filepath.Join(workspace, "install", "application.bin")
	writeFileMode(t, source, []byte("old"), 0o600)
	writeFileMode(t, target, []byte("new"), 0o755)
	writeFileMode(t, installed, []byte("old"), 0o640)
	patchPath := filepath.Join(workspace, "permissions.vipr")
	createPatch(t, patchPath, []FilePair{{SourcePath: source, TargetPath: target}}, true, "", nil)

	if err := Apply(context.Background(), patchPath, filepath.Dir(installed), Forward, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if actual := info.Mode().Perm(); actual != 0o640 {
		t.Fatalf("installed mode = %#o, want 0640", actual)
	}
}

func TestApplyReportsChmodFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("VIPR portable modes are intentionally ignored on Windows")
	}
	fixture := newSingleFileFixture(t, false)
	expected := errors.New("chmod failed")
	err := applyWithOperations(context.Background(), ApplyOptions{PatchPath: fixture.patchPath, Root: fixture.installRoot, Direction: Forward}, nil, applyOperations{
		chmod: func(*os.File, os.FileMode) error { return expected },
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
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	digest, size, hashError := hashutil.Reader(file)
	closeError := file.Close()
	if hashError != nil {
		t.Fatal(hashError)
	}
	if closeError != nil {
		t.Fatal(closeError)
	}
	return fileExpectation{Identity: info, Hash: digest, Size: size}
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

func TestCreateRejectsOutputInputCollision(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "source.vipr")
	target := filepath.Join(workspace, "target.vipr")
	writeFile(t, source, []byte("source"))
	writeFile(t, target, []byte("target"))

	for _, test := range []struct {
		name       string
		outputPath string
		want       string
	}{
		{name: "source", outputPath: source, want: "must not replace source"},
		{name: "target", outputPath: target, want: "must not replace target"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := Create(context.Background(), CreateOptions{
				Files:            []FilePair{{SourcePath: source, TargetPath: target}},
				OutputPath:       test.outputPath,
				CompressionLevel: 3,
			}, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	assertFile(t, source, []byte("source"))
	assertFile(t, target, []byte("target"))

	caseEquivalentOutput := filepath.Join(workspace, "SOURCE.VIPR")
	err := Create(context.Background(), CreateOptions{
		Files:            []FilePair{{SourcePath: source, TargetPath: target}},
		OutputPath:       caseEquivalentOutput,
		CompressionLevel: 3,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "must not replace source") {
		t.Fatalf("case-insensitive collision error = %v", err)
	}

	unicodeSource := filepath.Join(workspace, "é.vipr")
	writeFile(t, unicodeSource, []byte("unicode-source"))
	unicodeOutput := filepath.Join(workspace, "e\u0301.vipr")
	err = Create(context.Background(), CreateOptions{
		Files:            []FilePair{{SourcePath: unicodeSource, TargetPath: target}},
		OutputPath:       unicodeOutput,
		CompressionLevel: 3,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "must not replace source") {
		t.Fatalf("Unicode-equivalent output collision error = %v", err)
	}

	hardLink := filepath.Join(workspace, "hard-link.vipr")
	if err := os.Link(source, hardLink); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}
	err = Create(context.Background(), CreateOptions{
		Files:            []FilePair{{SourcePath: source, TargetPath: target}},
		OutputPath:       hardLink,
		CompressionLevel: 3,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "must not replace source") {
		t.Fatalf("hard-link collision error = %v", err)
	}
}

func TestCreateRejectsUnicodeEquivalentPaths(t *testing.T) {
	workspace := t.TempDir()
	sourceRoot := filepath.Join(workspace, "old")
	targetRoot := filepath.Join(workspace, "new")
	composed := filepath.Join(sourceRoot, "é.txt")
	decomposed := filepath.Join(sourceRoot, "e\u0301.txt")
	writeFile(t, composed, []byte("one"))
	writeFile(t, decomposed, []byte("two"))
	if infoOne, errOne := os.Stat(composed); errOne != nil {
		t.Fatal(errOne)
	} else if infoTwo, errTwo := os.Stat(decomposed); errTwo != nil {
		t.Fatal(errTwo)
	} else if os.SameFile(infoOne, infoTwo) {
		t.Skip("filesystem normalizes the two Unicode names to one file")
	}
	targetOne := filepath.Join(targetRoot, "one.txt")
	targetTwo := filepath.Join(targetRoot, "two.txt")
	writeFile(t, targetOne, []byte("one-new"))
	writeFile(t, targetTwo, []byte("two-new"))

	err := Create(context.Background(), CreateOptions{
		Files: []FilePair{
			{SourcePath: composed, TargetPath: targetOne},
			{SourcePath: decomposed, TargetPath: targetTwo},
		},
		OutputPath:       filepath.Join(workspace, "unicode.vipr"),
		CompressionLevel: 3,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "Unicode-equivalent") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEstimateCreateAndSelectedWorkDirectory(t *testing.T) {
	workspace := t.TempDir()
	workParent := filepath.Join(workspace, "work")
	if err := os.Mkdir(workParent, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(workspace, "old", "application.bin")
	target := filepath.Join(workspace, "new", "application.bin")
	output := filepath.Join(workspace, "update.vipr")
	writeFile(t, source, []byte(strings.Repeat("source", 1024)))
	writeFile(t, target, []byte(strings.Repeat("target", 2048)))
	writeFile(t, output, []byte("existing"))
	options := CreateOptions{
		Files:            []FilePair{{SourcePath: source, TargetPath: target}},
		OutputPath:       output,
		CompressionLevel: 3,
		CreateReverse:    true,
		WorkDirectory:    workParent,
		Parallelism:      1,
	}
	estimate, err := EstimateCreate(options)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.SnapshotBytes == 0 || estimate.DifferentialBytes == 0 || estimate.ExistingOutputBytes != uint64(len("existing")) || estimate.TotalBytes < estimate.WorkDirectoryBytes {
		t.Fatalf("unexpected estimate: %#v", estimate)
	}
	observedWorkDirectory := false
	if err := Create(context.Background(), options, func(event progress.Event) {
		if event.Stage != progress.StageSnapshotting {
			return
		}
		entries, readErr := os.ReadDir(workParent)
		if readErr != nil {
			t.Errorf("read work directory: %v", readErr)
			return
		}
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "viper-patcher-create-") {
				observedWorkDirectory = true
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !observedWorkDirectory {
		t.Fatal("creator did not use the selected work directory")
	}
}

func TestCreateWithParallelFileProcessing(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("parallel test requires at least two logical CPUs")
	}
	workspace := t.TempDir()
	pairs := make([]FilePair, 0, 4)
	installRoot := filepath.Join(workspace, "install")
	for index := range 4 {
		name := fmt.Sprintf("file-%d.bin", index)
		source := filepath.Join(workspace, "old", name)
		target := filepath.Join(workspace, "new", name)
		oldData := []byte(strings.Repeat(fmt.Sprintf("old-%d-", index), 1024))
		newData := []byte(strings.Repeat(fmt.Sprintf("new-%d-", index), 1024))
		writeFile(t, source, oldData)
		writeFile(t, target, newData)
		writeFile(t, filepath.Join(installRoot, name), oldData)
		pairs = append(pairs, FilePair{SourcePath: source, TargetPath: target})
	}
	patchPath := filepath.Join(workspace, "parallel.vipr")
	if err := Create(context.Background(), CreateOptions{
		Files:            pairs,
		OutputPath:       patchPath,
		CompressionLevel: 3,
		Parallelism:      2,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), patchPath, installRoot, Forward, nil); err != nil {
		t.Fatal(err)
	}
	for index := range 4 {
		name := fmt.Sprintf("file-%d.bin", index)
		expected := []byte(strings.Repeat(fmt.Sprintf("new-%d-", index), 1024))
		assertFile(t, filepath.Join(installRoot, name), expected)
	}
}

func TestTransactionReturnsCommittedWarningForBackupCleanup(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "target.bin")
	temporary := filepath.Join(workspace, "replacement.bin")
	writeFile(t, target, []byte("old"))
	writeFile(t, temporary, []byte("new"))
	operations := defaultTransactionOperations
	renameCalls := 0
	operations.rename = func(oldPath, newPath string) error {
		renameCalls++
		return os.Rename(oldPath, newPath)
	}
	expected := errors.New("backup cleanup failed")
	operations.remove = func(path string) error {
		if renameCalls >= 2 && strings.Contains(filepath.Base(path), ".viper-patcher-backup-") {
			return expected
		}
		return os.Remove(path)
	}
	transaction := newTransactionWithOperations(operations)
	if err := transaction.Add(target, temporary, expectationFor(t, target)); err != nil {
		t.Fatal(err)
	}
	err := transaction.Commit()
	if !IsCommittedWarning(err) || !errors.Is(err, expected) {
		t.Fatalf("error = %v, want committed warning", err)
	}
	assertFile(t, target, []byte("new"))
}

func TestTransactionErrorWrappers(t *testing.T) {
	if err := wrapRemoveError("remove", "unused", nil); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if err := wrapRemoveError("remove", missing, os.Remove(missing)); err != nil {
		t.Fatalf("missing path should be ignored: %v", err)
	}
	expected := errors.New("remove failed")
	if err := wrapRemoveError("remove", "file", expected); !errors.Is(err, expected) {
		t.Fatalf("wrapped error = %v", err)
	}
}

func TestOperationErrorHelpers(t *testing.T) {
	if wrapOperationError("append", "file", nil) != nil {
		t.Fatal("nil operation error must remain nil")
	}
	expected := errors.New("I/O failed")
	if err := wrapOperationError("append", "file", expected); !errors.Is(err, expected) || !strings.Contains(err.Error(), "append") {
		t.Fatalf("wrapped operation error = %v", err)
	}
}
