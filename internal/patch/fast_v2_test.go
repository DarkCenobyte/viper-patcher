package patch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestFormatV2SelectsAndAppliesFastMethods(t *testing.T) {
	tests := []struct {
		name       string
		source     []byte
		target     func([]byte) []byte
		wantMethod string
	}{
		{
			name:   "sparse",
			source: bytes.Repeat([]byte("abcdefgh"), 128*1024),
			target: func(source []byte) []byte {
				target := append([]byte(nil), source...)
				for index := 0; index < len(target); index += 1000 {
					target[index] ^= 0x5a
				}
				return target
			},
			wantMethod: patchformat.MethodSparse,
		},
		{
			name:       "replace",
			source:     bytes.Repeat([]byte{1}, 1<<20),
			target:     func(source []byte) []byte { return bytes.Repeat([]byte{2}, len(source)) },
			wantMethod: patchformat.MethodReplace,
		},
		{
			name:   "copy-add",
			source: bytes.Repeat([]byte("abcdefgh"), 128*1024),
			target: func(source []byte) []byte {
				target := make([]byte, 0, len(source)+8192)
				target = append(target, source[:len(source)/2]...)
				target = append(target, bytes.Repeat([]byte("inserted"), 1024)...)
				target = append(target, source[len(source)/2:]...)
				return target
			},
			wantMethod: patchformat.MethodCopyAdd,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			targetData := test.target(test.source)
			workspace := t.TempDir()
			sourcePath := filepath.Join(workspace, "old", "game.bin")
			targetPath := filepath.Join(workspace, "new", "game.bin")
			installRoot := filepath.Join(workspace, "install")
			installedPath := filepath.Join(installRoot, "game.bin")
			patchPath := filepath.Join(workspace, "update.vipr")
			writeFile(t, sourcePath, test.source)
			writeFile(t, targetPath, targetData)
			writeFile(t, installedPath, test.source)

			if err := Create(context.Background(), CreateOptions{
				Files:            []FilePair{{SourcePath: sourcePath, TargetPath: targetPath}},
				OutputPath:       patchPath,
				CompressionLevel: 3,
				CreateReverse:    true,
				Parallelism:      1,
			}, nil); err != nil {
				t.Fatal(err)
			}
			parsed, err := Open(patchPath)
			if err != nil {
				t.Fatal(err)
			}
			if got := parsed.Header.Files[0].ForwardDifferentialMethod(); got != test.wantMethod {
				t.Fatalf("forward method = %q, want %q", got, test.wantMethod)
			}
			if parsed.Header.FormatVersion != patchformat.FormatVersion {
				t.Fatalf("format version = %d", parsed.Header.FormatVersion)
			}

			if err := ApplyWithOptions(context.Background(), ApplyOptions{
				PatchPath:   patchPath,
				Root:        installRoot,
				Direction:   Forward,
				Parallelism: 2,
			}, nil); err != nil {
				t.Fatal(err)
			}
			assertFile(t, installedPath, targetData)
			if err := ApplyWithOptions(context.Background(), ApplyOptions{
				PatchPath:   patchPath,
				Root:        installRoot,
				Direction:   Reverse,
				Parallelism: 2,
			}, nil); err != nil {
				t.Fatal(err)
			}
			assertFile(t, installedPath, test.source)
		})
	}
}

func TestFormatV2SparsePatchIsSmallerThanChangedFile(t *testing.T) {
	workspace := t.TempDir()
	sourceData := bytes.Repeat([]byte("0123456789abcdef"), 64*1024)
	targetData := append([]byte(nil), sourceData...)
	for index := 0; index < len(targetData); index += 1000 {
		targetData[index] ^= 0x7f
	}
	sourcePath := filepath.Join(workspace, "old", "data.bin")
	targetPath := filepath.Join(workspace, "new", "data.bin")
	patchPath := filepath.Join(workspace, "update.vipr")
	writeFile(t, sourcePath, sourceData)
	writeFile(t, targetPath, targetData)
	if err := Create(context.Background(), CreateOptions{
		Files:            []FilePair{{SourcePath: sourcePath, TargetPath: targetPath}},
		OutputPath:       patchPath,
		CompressionLevel: 3,
		Parallelism:      1,
	}, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= int64(len(sourceData)/10) {
		t.Fatalf("sparse patch is unexpectedly large: %d bytes", info.Size())
	}
}

func TestFormatV2ParallelApplyUsesIndependentPatchOffsets(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("parallel test requires at least two logical CPUs")
	}
	workspace := t.TempDir()
	installRoot := filepath.Join(workspace, "install")
	pairs := make([]FilePair, 0, 12)
	expected := make(map[string][]byte, 12)
	for index := 0; index < 12; index++ {
		sourceData := bytes.Repeat([]byte{byte(index), 1, 2, 3}, 32*1024)
		targetData := append([]byte(nil), sourceData...)
		for offset := index; offset < len(targetData); offset += 997 {
			targetData[offset] ^= 0x5a
		}
		name := fmt.Sprintf("file-%02d.bin", index)
		sourcePath := filepath.Join(workspace, "old", name)
		targetPath := filepath.Join(workspace, "new", name)
		writeFile(t, sourcePath, sourceData)
		writeFile(t, targetPath, targetData)
		writeFile(t, filepath.Join(installRoot, name), sourceData)
		pairs = append(pairs, FilePair{SourcePath: sourcePath, TargetPath: targetPath})
		expected[name] = targetData
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
	if err := ApplyWithOptions(context.Background(), ApplyOptions{
		PatchPath:   patchPath,
		Root:        installRoot,
		Direction:   Forward,
		Parallelism: 2,
	}, nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range expected {
		assertFile(t, filepath.Join(installRoot, name), data)
	}
}

func TestOpenWithDigestReturnsPhysicalFileSHA256(t *testing.T) {
	fixture := newSingleFileFixture(t, false)
	contents, err := os.ReadFile(fixture.patchPath)
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256(contents)
	_, digest, err := OpenWithDigest(fixture.patchPath)
	if err != nil {
		t.Fatal(err)
	}
	if digest != fmt.Sprintf("%x", expected) {
		t.Fatalf("digest = %s, want %x", digest, expected)
	}
}

func TestApplyRejectsHeaderChangedAfterInspection(t *testing.T) {
	fixture := newSingleFileFixture(t, false)
	_, digest, err := OpenWithDigest(fixture.patchPath)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(fixture.patchPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{'X'}, 0); err != nil {
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
	if err == nil || err.Error() != "selected patch changed after it was inspected" {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFile(t, fixture.installedPath, fixture.sourceData)
}
