package patch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

type v4APIFixture struct {
	directory  string
	sourcePath string
	targetPath string
	patchPath  string
	source     []byte
	target     []byte
}

func newV4APIFixture(t *testing.T, name string, reverse bool) v4APIFixture {
	t.Helper()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source", name)
	targetPath := filepath.Join(directory, "target", "renamed-"+name)
	for _, path := range []string{sourcePath, targetPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source := make([]byte, 768<<10)
	for index := range source {
		source[index] = byte(index*29 + index/97)
	}
	target := append([]byte(nil), source...)
	for index := 192 << 10; index < 320<<10; index++ {
		target[index] ^= byte(index*13 + 7)
	}
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, target, 0o600); err != nil {
		t.Fatal(err)
	}
	patchPath := filepath.Join(directory, name+".vipr")
	if err := Create(context.Background(), CreateOptions{
		Files:            []FilePair{{SourcePath: sourcePath, TargetPath: targetPath}},
		OutputPath:       patchPath,
		CompressionLevel: 3,
		Comment:          "API coverage",
		CreateReverse:    reverse,
		WindowSize:       256 << 10,
		Optimization:     OptimizeBalanced,
		WorkerBudget:     2,
	}, nil); err != nil {
		t.Fatal(err)
	}
	return v4APIFixture{directory, sourcePath, targetPath, patchPath, source, target}
}

func TestV4OpenPrepareInspectAndApplyAPIs(t *testing.T) {
	fixture := newV4APIFixture(t, "application.bin", true)
	parsed, err := Open(fixture.patchPath)
	if err != nil {
		t.Fatal(err)
	}
	parsedWithDigest, digest, err := OpenWithDigest(fixture.patchPath)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || len(parsed.Header.Files) != 1 || parsedWithDigest.Header.Files[0].Path != "application.bin" {
		t.Fatalf("unexpected open result: digest=%q parsed=%+v", digest, parsedWithDigest)
	}

	prepared, err := Prepare(fixture.patchPath)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Path() != fixture.patchPath {
		t.Fatalf("prepared path = %q", prepared.Path())
	}
	preparedDigest, err := prepared.Digest()
	if err != nil || preparedDigest != digest {
		t.Fatalf("prepared digest = %q, %v; want %q", preparedDigest, err, digest)
	}
	firstParsed, err := prepared.Parsed()
	if err != nil {
		t.Fatal(err)
	}
	firstParsed.Header.Files[0].Path = "mutated.bin"
	firstParsed.Header.Files[0].SourceChunks[0][0] ^= 0xff
	firstParsed.Header.Files[0].ForwardWindows[0].OutputSize++
	secondParsed, err := prepared.Parsed()
	if err != nil {
		t.Fatal(err)
	}
	if secondParsed.Header.Files[0].Path != "application.bin" || secondParsed.Header.Files[0].SourceChunks[0] != parsed.Header.Files[0].SourceChunks[0] || secondParsed.Header.Files[0].ForwardWindows[0].OutputSize != parsed.Header.Files[0].ForwardWindows[0].OutputSize {
		t.Fatal("PreparedPatch.Parsed returned aliased data")
	}

	installRoot := filepath.Join(fixture.directory, "install")
	if err := os.Mkdir(installRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(installRoot, "application.bin")
	if err := os.WriteFile(installed, fixture.source, 0o640); err != nil {
		t.Fatal(err)
	}
	inspection, err := Inspect(installRoot, parsed)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != StateForwardReady || !inspection.Ready(Forward) || inspection.Ready(Reverse) {
		t.Fatalf("source inspection = %+v", inspection)
	}

	if err := Apply(context.Background(), fixture.patchPath, installRoot, Forward, nil); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, fixture.target) {
		t.Fatal("Apply did not produce the target")
	}
	inspection, err = InspectContext(context.Background(), installRoot, parsed)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.State != StateReverseReady || !inspection.Ready(Reverse) {
		t.Fatalf("target inspection = %+v", inspection)
	}

	if err := ApplyPreparedWithOptions(context.Background(), prepared, PreparedApplyOptions{
		Root:         installRoot,
		Direction:    Reverse,
		WorkerBudget: 2,
		Verify:       VerifyOutput,
		Durability:   DurabilityDurable,
		IOProfile:    IOSSD,
	}, nil); err != nil {
		t.Fatal(err)
	}
	actual, err = os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, fixture.source) {
		t.Fatal("prepared reverse application did not restore the source")
	}

	if err := ApplyWithOptions(context.Background(), ApplyOptions{
		PatchPath:         fixture.patchPath,
		Root:              installRoot,
		Direction:         Forward,
		ExpectedPatchHash: digest,
		Verify:            VerifyStrict,
		Durability:        DurabilityBuffered,
		IOProfile:         IONVMe,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ApplyWithOptions(context.Background(), ApplyOptions{
		PatchPath:         fixture.patchPath,
		Root:              installRoot,
		Direction:         Reverse,
		ExpectedPatchHash: strings.Repeat("0", 64),
	}, nil); err == nil || !strings.Contains(err.Error(), "changed after inspection") {
		t.Fatalf("wrong expected patch digest error = %v", err)
	}

	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Digest(); err == nil {
		t.Fatal("Digest succeeded after Close")
	}
	if _, err := prepared.Parsed(); err == nil {
		t.Fatal("Parsed succeeded after Close")
	}
	if _, _, err := prepared.acquire(); err == nil {
		t.Fatal("acquire succeeded after Close")
	}
}

func TestV4PreparedPatchNilAndMutationHandling(t *testing.T) {
	var prepared *PreparedPatch
	if prepared.Path() != "" || prepared.Close() != nil {
		t.Fatal("nil PreparedPatch is not inert")
	}
	if _, err := prepared.Digest(); err == nil {
		t.Fatal("nil PreparedPatch.Digest succeeded")
	}
	if _, err := prepared.Parsed(); err == nil {
		t.Fatal("nil PreparedPatch.Parsed succeeded")
	}
	if _, _, err := prepared.acquire(); err == nil {
		t.Fatal("nil PreparedPatch.acquire succeeded")
	}

	fixture := newV4APIFixture(t, "mutable.bin", false)
	prepared, err := Prepare(fixture.patchPath)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	info, err := os.Stat(fixture.patchPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(fixture.patchPath, changed, changed); err != nil {
		t.Fatal(err)
	}
	installRoot := filepath.Join(fixture.directory, "install")
	if err := os.Mkdir(installRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installRoot, "mutable.bin"), fixture.source, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPreparedWithOptions(context.Background(), prepared, PreparedApplyOptions{Root: installRoot, Direction: Forward}, nil); err == nil || !strings.Contains(err.Error(), "changed during operation") {
		t.Fatalf("mutated prepared patch error = %v", err)
	}

	var opened *openedPatch
	if err := opened.verifyStable(); err == nil {
		t.Fatal("nil opened patch verified")
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestV4OpenRejectsInvalidContainers(t *testing.T) {
	directory := t.TempDir()
	for name, data := range map[string][]byte{
		"empty.vipr":     nil,
		"truncated.vipr": []byte("VIPR\r\n\x1a\x04"),
		"wrong.vipr":     []byte("not-a-patch"),
	} {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); err == nil {
			t.Fatalf("Open(%s) unexpectedly succeeded", name)
		}
	}
	if _, err := Open(filepath.Join(directory, "missing.vipr")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing patch error = %v", err)
	}
}

func TestV4InspectStates(t *testing.T) {
	first := newV4APIFixture(t, "one.bin", true)
	second := newV4APIFixture(t, "two.bin", true)
	firstPatch, err := Open(first.patchPath)
	if err != nil {
		t.Fatal(err)
	}
	secondPatch, err := Open(second.patchPath)
	if err != nil {
		t.Fatal(err)
	}
	firstEntry := firstPatch.Header.Files[0]
	secondEntry := secondPatch.Header.Files[0]
	firstEntry.Path = "one.bin"
	secondEntry.Path = "two.bin"
	parsed := patchformat.Patch{Header: firstPatch.Header}
	parsed.Header.Files = []patchformat.FileEntry{firstEntry, secondEntry}
	parsed.Header.Reverse = true

	missingRoot := t.TempDir()
	result, err := Inspect(missingRoot, parsed)
	if err != nil || result.State != StateMissingFiles || len(result.Missing) != 2 {
		t.Fatalf("missing inspection = %+v, %v", result, err)
	}

	mixedRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(mixedRoot, "one.bin"), first.source, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mixedRoot, "two.bin"), second.target, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = Inspect(mixedRoot, parsed)
	if err != nil || result.State != StateMixedFiles || result.CanApplyForward || result.CanApplyReverse || len(result.Issues) != 0 {
		t.Fatalf("mixed inspection = %+v, %v", result, err)
	}

	invalidRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(invalidRoot, "one.bin"), bytes.Repeat([]byte{0xaa}, len(first.source)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalidRoot, "two.bin"), bytes.Repeat([]byte{0xbb}, len(second.source)), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = Inspect(invalidRoot, parsed)
	if err != nil || result.State != StateInvalidFiles || len(result.Issues) != 2 || result.Issues[0].Reason != IssueHashMismatch {
		t.Fatalf("invalid inspection = %+v, %v", result, err)
	}

	nonRegularRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(nonRegularRoot, "one.bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	single := patchformat.Patch{Header: parsed.Header}
	single.Header.Files = []patchformat.FileEntry{firstEntry}
	result, err = Inspect(nonRegularRoot, single)
	if err != nil || result.State != StateInvalidFiles || len(result.Issues) != 1 || result.Issues[0].Reason != IssueNotRegular {
		t.Fatalf("non-regular inspection = %+v, %v", result, err)
	}

	bidirectionalRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(bidirectionalRoot, "one.bin"), first.source, 0o600); err != nil {
		t.Fatal(err)
	}
	identical := firstEntry
	identical.TargetSize = identical.SourceSize
	identical.TargetDigest = identical.SourceDigest
	identical.TargetHash = identical.SourceHash
	identical.TargetChunks = append([]patchformat.Digest(nil), identical.SourceChunks...)
	both := patchformat.Patch{Header: parsed.Header}
	both.Header.Files = []patchformat.FileEntry{identical}
	both.Header.Reverse = true
	result, err = Inspect(bidirectionalRoot, both)
	if err != nil || result.State != StateBidirectionalReady || !result.CanApplyForward || !result.CanApplyReverse {
		t.Fatalf("bidirectional inspection = %+v, %v", result, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectContext(ctx, mixedRoot, parsed); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled inspection error = %v", err)
	}
	fileRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(fileRoot, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(fileRoot, parsed); err == nil {
		t.Fatal("Inspect accepted a file as installation root")
	}
}
