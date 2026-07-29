//go:build ignore

package patch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestPreparedPatchReusesStableHandleAndClonesMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prepared.vipr")
	writePreparedPatchFixture(t, path)

	prepared, err := Prepare(path)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()

	first, release, err := prepared.acquire()
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	second, release, err := prepared.acquire()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("prepared patch did not reuse the same opened handle")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}

	parsed, err := prepared.Parsed()
	if err != nil {
		t.Fatal(err)
	}
	parsed.Header.Files[0].Path = "mutated.bin"
	parsedAgain, err := prepared.Parsed()
	if err != nil {
		t.Fatal(err)
	}
	if parsedAgain.Header.Files[0].Path != "file.bin" {
		t.Fatal("prepared metadata was mutated through a returned copy")
	}
}

func TestPreparedPatchRejectsPathReplacementBeforeApply(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "prepared.vipr")
	writePreparedPatchFixture(t, path)
	prepared, err := Prepare(path)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()

	replacement := filepath.Join(directory, "replacement.vipr")
	writePreparedPatchFixture(t, replacement)
	if err := os.Rename(replacement, path); err != nil {
		// Windows normally denies replacement while the prepared read handle is
		// open. That is stronger than the identity check this test exercises on
		// rename-capable platforms, so verify that the prepared handle remains
		// usable instead of treating the OS protection as a test failure.
		if runtime.GOOS == "windows" {
			opened, release, acquireError := prepared.acquire()
			if acquireError != nil || opened == nil {
				t.Fatalf("prepared patch became unusable after blocked replacement: %v", acquireError)
			}
			if releaseError := release(); releaseError != nil {
				t.Fatal(releaseError)
			}
			return
		}
		t.Fatal(err)
	}
	if _, _, err := prepared.acquire(); err == nil {
		t.Fatal("expected path replacement to invalidate prepared patch")
	}
}

func TestPreparedPatchRejectsUseAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prepared.vipr")
	writePreparedPatchFixture(t, path)
	prepared, err := Prepare(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Parsed(); err == nil {
		t.Fatal("expected closed prepared patch to reject metadata access")
	}
	if _, _, err := prepared.acquire(); err == nil {
		t.Fatal("expected closed prepared patch to reject acquisition")
	}
}

func writePreparedPatchFixture(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	header := patchformat.Header{
		FormatVersion: patchformat.FormatVersion,
		CreatedAt:     time.Unix(0, 0).UTC(),
		Creator:       patchformat.CreatorInfo{Name: "test", Version: "test"},
		HashAlgorithm: patchformat.HashBLAKE3Tree,
		Compression: patchformat.Compression{
			Algorithm: patchformat.AlgorithmHybrid,
			Library:   patchformat.SupportedZstdVersion,
			Mode:      patchformat.CompressionHybrid,
			Level:     3,
		},
		Files: []patchformat.FileEntry{{
			Path:          "file.bin",
			SourceHash:    strings.Repeat("0", 64),
			TargetHash:    strings.Repeat("1", 64),
			ForwardMethod: patchformat.MethodReplace,
			ForwardLength: 1,
		}},
	}
	if _, err := patchformat.EncodePrefix(file, header); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
