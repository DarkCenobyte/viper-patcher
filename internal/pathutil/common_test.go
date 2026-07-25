package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCommonBaseAndRelativePatchPath(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "bin", "game.exe")
	second := filepath.Join(root, "data", "assets.bin")
	base, err := CommonBase([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if base != root {
		t.Fatalf("base = %q, want %q", base, root)
	}
	relative, err := RelativePatchPath(base, second)
	if err != nil {
		t.Fatal(err)
	}
	if relative != "data/assets.bin" {
		t.Fatalf("relative = %q", relative)
	}
}

func TestCommonBaseRequiresFiles(t *testing.T) {
	if _, err := CommonBase(nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSecureJoin(t *testing.T) {
	root := t.TempDir()
	joined, err := SecureJoin(root, "data/file.bin")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "data", "file.bin")
	if joined != want {
		t.Fatalf("joined = %q, want %q", joined, want)
	}
	for _, unsafe := range []string{"", "../escape", "/absolute", "a/../../escape"} {
		if _, err := SecureJoin(root, unsafe); err == nil {
			t.Fatalf("expected %q to be rejected", unsafe)
		}
	}
}

func TestSecureJoinExistingRejectsSymbolicLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link creation normally requires additional privileges on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file.bin"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := SecureJoinExisting(root, "linked/file.bin"); err == nil {
		t.Fatal("expected symbolic-link traversal to be rejected")
	}
}

func TestSecureJoinExistingAllowsMissingFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	joined, err := SecureJoinExisting(root, "data/missing.bin")
	if err != nil {
		t.Fatal(err)
	}
	if joined != filepath.Join(root, "data", "missing.bin") {
		t.Fatalf("joined = %q", joined)
	}
}

func TestSecureJoinExistingRejectsSymbolicLinkRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link creation normally requires additional privileges on Windows")
	}
	realRoot := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := SecureJoinExisting(linkRoot, "file.bin"); err == nil {
		t.Fatal("expected symbolic-link root to be rejected")
	}
}

func FuzzSecureJoin(f *testing.F) {
	f.Add("data/file.bin")
	f.Add("../escape")
	f.Add(`data\file.bin`)
	f.Fuzz(func(t *testing.T, patchPath string) {
		root := t.TempDir()
		joined, err := SecureJoin(root, patchPath)
		if err == nil && !isWithin(root, joined) {
			t.Fatalf("joined path escaped root: %q", joined)
		}
	})
}
