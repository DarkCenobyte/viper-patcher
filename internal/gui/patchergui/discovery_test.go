package patchergui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdjacentPatchPathSelectsExactlyOnePatch(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "patcher.exe")
	if err := os.WriteFile(executable, []byte("executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	patchPath := filepath.Join(directory, "update.vipr")
	if err := os.WriteFile(patchPath, []byte("patch"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, found, err := adjacentPatchPath(executable)
	if err != nil {
		t.Fatal(err)
	}
	if !found || path != patchPath {
		t.Fatalf("path = %q, found = %v", path, found)
	}
}

func TestAdjacentPatchPathRejectsMultiplePatches(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "patcher.exe")
	for _, name := range []string{"first.vipr", "second.VIPR"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("patch"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path, found, err := adjacentPatchPath(executable)
	if err != nil {
		t.Fatal(err)
	}
	if found || path != "" {
		t.Fatalf("path = %q, found = %v", path, found)
	}
}

func TestAdjacentPatchPathIgnoresSubdirectories(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "patcher.exe")
	if err := os.Mkdir(filepath.Join(directory, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "nested", "update.vipr"), []byte("patch"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, found, err := adjacentPatchPath(executable)
	if err != nil {
		t.Fatal(err)
	}
	if found || path != "" {
		t.Fatalf("path = %q, found = %v", path, found)
	}
}
