package patch

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestV4ForwardReverseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "game.bin")
	newPath := filepath.Join(dir, "new.bin")
	source := make([]byte, 3<<20)
	rand.New(rand.NewSource(1)).Read(source)
	target := append([]byte(nil), source...)
	for i := 0; i < 4096; i++ {
		target[(i*733)%len(target)] ^= byte(i + 1)
	}
	if err := os.WriteFile(oldPath, source, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, target, 0600); err != nil {
		t.Fatal(err)
	}
	patchPath := filepath.Join(dir, "update.vipr")
	if err := Create(context.Background(), CreateOptions{Files: []FilePair{{oldPath, newPath}}, OutputPath: patchPath, CompressionLevel: 3, CreateReverse: true, WindowSize: 256 << 10, Optimization: OptimizeBalanced, WorkerBudget: 4}, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "game.bin"), source, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyWithOptions(context.Background(), ApplyOptions{PatchPath: patchPath, Root: root, Direction: Forward, Verify: VerifyReferenced, WorkerBudget: 4}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "game.bin"))
	if !bytes.Equal(got, target) {
		t.Fatal("forward mismatch")
	}
	if err := ApplyWithOptions(context.Background(), ApplyOptions{PatchPath: patchPath, Root: root, Direction: Reverse, Verify: VerifyStrict, WorkerBudget: 4}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(root, "game.bin"))
	if !bytes.Equal(got, source) {
		t.Fatal("reverse mismatch")
	}
}
func TestV4WrongSourceRejected(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "a.bin")
	targetPath := filepath.Join(dir, "b.bin")
	os.WriteFile(sourcePath, bytes.Repeat([]byte{1}, 1<<20), 0600)
	os.WriteFile(targetPath, bytes.Repeat([]byte{2}, 1<<20), 0600)
	patchPath := filepath.Join(dir, "x.vipr")
	if err := Create(context.Background(), CreateOptions{Files: []FilePair{{sourcePath, targetPath}}, OutputPath: patchPath, CompressionLevel: 1}, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "a.bin"), bytes.Repeat([]byte{3}, 1<<20), 0600)
	if err := ApplyWithOptions(context.Background(), ApplyOptions{PatchPath: patchPath, Root: root, Direction: Forward, Verify: VerifyStrict}, nil); err == nil {
		t.Fatal("expected source mismatch")
	}
}

func TestV4CloneEligibleRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "clone.bin")
	targetPath := filepath.Join(dir, "clone-new.bin")
	source := make([]byte, 16<<20)
	if _, err := rand.New(rand.NewSource(7)).Read(source); err != nil {
		t.Fatal(err)
	}
	target := append([]byte(nil), source...)
	for index := 6 << 20; index < 6<<20+256<<10; index++ {
		target[index] ^= byte(index*31 + 17)
	}
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, target, 0o600); err != nil {
		t.Fatal(err)
	}
	patchPath := filepath.Join(dir, "clone.vipr")
	if err := Create(context.Background(), CreateOptions{
		Files:            []FilePair{{sourcePath, targetPath}},
		OutputPath:       patchPath,
		CompressionLevel: 3,
		CreateReverse:    true,
		WindowSize:       1 << 20,
		Optimization:     OptimizeApplySpeed,
		WorkerBudget:     4,
	}, nil); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(root, "clone.bin")
	if err := os.WriteFile(installed, source, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyWithOptions(context.Background(), ApplyOptions{
		PatchPath:    patchPath,
		Root:         root,
		Direction:    Forward,
		Verify:       VerifyReferenced,
		WorkerBudget: 4,
	}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, target) {
		t.Fatal("clone-eligible forward mismatch")
	}
	if err := ApplyWithOptions(context.Background(), ApplyOptions{
		PatchPath:    patchPath,
		Root:         root,
		Direction:    Reverse,
		Verify:       VerifyReferenced,
		WorkerBudget: 4,
	}, nil); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, source) {
		t.Fatal("clone-eligible reverse mismatch")
	}
}

func TestV4CommitRollsBackEarlierFiles(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.bin")
	secondPath := filepath.Join(dir, "second.bin")
	if err := os.WriteFile(firstPath, []byte("first-old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("second-old"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := openInstallationRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	firstSource, firstIdentity, firstName, err := root.openStableRegularFile("first.bin")
	if err != nil {
		t.Fatal(err)
	}
	secondSource, secondIdentity, secondName, err := root.openStableRegularFile("second.bin")
	if err != nil {
		firstSource.Close()
		t.Fatal(err)
	}
	if err := firstSource.Close(); err != nil {
		secondSource.Close()
		t.Fatal(err)
	}
	if err := secondSource.Close(); err != nil {
		t.Fatal(err)
	}
	firstTempFile, firstTemp, err := createRootTemp(root.root, ".", ".viper-test-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstTempFile.Write([]byte("first-new")); err != nil {
		firstTempFile.Close()
		t.Fatal(err)
	}
	if err := firstTempFile.Close(); err != nil {
		t.Fatal(err)
	}
	missingTemp := filepath.Join(".", ".viper-missing-output")
	err = commitPrepared(root, []preparedFile{
		{path: firstName, temp: firstTemp, identity: firstIdentity},
		{path: secondName, temp: missingTemp, identity: secondIdentity},
	}, DurabilityBuffered)
	if err == nil {
		t.Fatal("expected the missing second prepared file to fail the commit")
	}
	first, readErr := os.ReadFile(firstPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	second, readErr := os.ReadFile(secondPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(first) != "first-old" || string(second) != "second-old" {
		t.Fatalf("rollback did not restore installation: first=%q second=%q", first, second)
	}
}
