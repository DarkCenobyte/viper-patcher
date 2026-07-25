package patch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func TestCreateApplyAndReverse(t *testing.T) {
	workspace := t.TempDir()
	sourceRoot := filepath.Join(workspace, "old")
	targetRoot := filepath.Join(workspace, "new")
	applyRoot := filepath.Join(workspace, "installed")
	for _, directory := range []string{
		filepath.Join(sourceRoot, "bin"), filepath.Join(sourceRoot, "data"),
		filepath.Join(targetRoot, "renamed"), filepath.Join(applyRoot, "bin"), filepath.Join(applyRoot, "data"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
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
	if err := Create(context.Background(), CreateOptions{
		SourceFiles:      []string{sourceGame, sourceAssets},
		TargetFiles:      []string{targetGame, targetAssets},
		OutputPath:       patchPath,
		CompressionLevel: 7,
		Comment:          "Install version 2.",
		CreateReverse:    true,
	}, nil); err != nil {
		t.Fatal(err)
	}
	parsed, err := Open(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Comment != "Install version 2." || !parsed.Header.Reverse || len(parsed.Header.Files) != 2 {
		t.Fatalf("unexpected header: %#v", parsed.Header)
	}
	if parsed.Header.Files[0].Path != "bin/game.exe" || parsed.Header.Files[1].Path != "data/assets.bin" {
		t.Fatalf("unexpected paths: %#v", parsed.Header.Files)
	}
	validation, err := Inspect(applyRoot, parsed)
	if err != nil || validation.State != StateForwardReady {
		t.Fatalf("forward validation = %#v, %v", validation, err)
	}
	completed := make(map[string]bool)
	if err := Apply(context.Background(), patchPath, applyRoot, Forward, func(event progress.Event) {
		if event.Stage == "file-completed" {
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
	if err != nil || validation.State != StateReverseReady {
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
	if err := Create(context.Background(), CreateOptions{
		SourceFiles: []string{source}, TargetFiles: []string{target}, OutputPath: output,
		CompressionLevel: 3, Comment: "replacement",
	}, nil); err != nil {
		t.Fatal(err)
	}
	parsed, err := Open(output)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header.Comment != "replacement" {
		t.Fatalf("comment = %q", parsed.Header.Comment)
	}
}

func TestInspectMixedState(t *testing.T) {
	workspace := t.TempDir()
	sourceRoot := filepath.Join(workspace, "old")
	targetRoot := filepath.Join(workspace, "new")
	installRoot := filepath.Join(workspace, "install")
	for _, name := range []string{"one.bin", "two.bin"} {
		writeFile(t, filepath.Join(sourceRoot, name), []byte("old-"+name))
		writeFile(t, filepath.Join(targetRoot, name), []byte("new-"+name))
	}
	writeFile(t, filepath.Join(installRoot, "one.bin"), []byte("old-one.bin"))
	writeFile(t, filepath.Join(installRoot, "two.bin"), []byte("new-two.bin"))
	patchPath := filepath.Join(workspace, "mixed.vipr")
	if err := Create(context.Background(), CreateOptions{
		SourceFiles: []string{filepath.Join(sourceRoot, "one.bin"), filepath.Join(sourceRoot, "two.bin")},
		TargetFiles: []string{filepath.Join(targetRoot, "one.bin"), filepath.Join(targetRoot, "two.bin")},
		OutputPath:  patchPath, CompressionLevel: 3, CreateReverse: true,
	}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := Inspect(installRoot, mustOpen(t, patchPath))
	if err != nil || result.State != StateHashMismatch || len(result.Mismatched) != 2 {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestInspectReportsMissingAndMismatch(t *testing.T) {
	workspace := t.TempDir()
	header := fakeHeader(t, workspace)
	parsed := mustOpen(t, header.patchPath)
	result, err := Inspect(header.root, parsed)
	if err != nil || result.State != StateMissingFiles || len(result.Missing) != 1 {
		t.Fatalf("missing result = %#v, %v", result, err)
	}
	writeFile(t, filepath.Join(header.root, "source.bin"), []byte("unknown"))
	result, err = Inspect(header.root, parsed)
	if err != nil || result.State != StateHashMismatch || len(result.Mismatched) != 1 {
		t.Fatalf("mismatch result = %#v, %v", result, err)
	}
}

func TestCreateRejectsMismatchedLists(t *testing.T) {
	err := Create(context.Background(), CreateOptions{SourceFiles: []string{"a"}, TargetFiles: nil, OutputPath: "x.vipr", CompressionLevel: 3}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
}

type fakePatch struct{ patchPath, root string }

func fakeHeader(t *testing.T, workspace string) fakePatch {
	t.Helper()
	source := filepath.Join(workspace, "source.bin")
	target := filepath.Join(workspace, "target.bin")
	root := filepath.Join(workspace, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, source, []byte("source"))
	writeFile(t, target, []byte("target"))
	patchPath := filepath.Join(workspace, "single.vipr")
	if err := Create(context.Background(), CreateOptions{SourceFiles: []string{source}, TargetFiles: []string{target}, OutputPath: patchPath, CompressionLevel: 3}, nil); err != nil {
		t.Fatal(err)
	}
	return fakePatch{patchPath: patchPath, root: root}
}

func mustOpen(t *testing.T, path string) patchformat.Patch {
	t.Helper()
	parsed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
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

func TestOpenRejectsTrailingData(t *testing.T) {
	workspace := t.TempDir()
	created := fakeHeader(t, workspace)
	file, err := os.OpenFile(created.patchPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("hidden")); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(created.patchPath); err == nil {
		t.Fatal("expected trailing data to be rejected")
	}
}

func TestInspectRejectsSymbolicLinkTraversal(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	workspace := t.TempDir()
	created := fakeHeader(t, workspace)
	outside := filepath.Join(workspace, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(outside, "source.bin"), []byte("source"))
	if err := os.Symlink(outside, filepath.Join(created.root, "link")); err != nil {
		t.Skipf("cannot create symbolic link: %v", err)
	}
	parsed := mustOpen(t, created.patchPath)
	parsed.Header.Files[0].Path = "link/source.bin"
	if _, err := Inspect(created.root, parsed); err == nil {
		t.Fatal("expected symbolic-link traversal to be rejected")
	}
}

func TestApplyRejectsUnavailableDirectionAndCancellation(t *testing.T) {
	workspace := t.TempDir()
	created := fakeHeader(t, workspace)
	writeFile(t, filepath.Join(created.root, "source.bin"), []byte("source"))
	if err := Apply(context.Background(), created.patchPath, created.root, Reverse, nil); err == nil {
		t.Fatal("expected reverse application without reverse data to fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Apply(ctx, created.patchPath, created.root, Forward, nil); err == nil {
		t.Fatal("expected canceled context to fail")
	}
}

func TestApplyRejectsHashMismatch(t *testing.T) {
	workspace := t.TempDir()
	created := fakeHeader(t, workspace)
	writeFile(t, filepath.Join(created.root, "source.bin"), []byte("unknown"))
	if err := Apply(context.Background(), created.patchPath, created.root, Forward, nil); err == nil {
		t.Fatal("expected hash mismatch to fail")
	}
}
