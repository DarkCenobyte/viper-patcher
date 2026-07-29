//go:build !windows

package patch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestV4RejectsSymlinkRootsAndComponents(t *testing.T) {
	directory := t.TempDir()
	realRoot := filepath.Join(directory, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	rootLink := filepath.Join(directory, "root-link")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Fatal(err)
	}
	if _, err := openInstallationRoot(rootLink); err == nil {
		t.Fatal("openInstallationRoot accepted a symbolic-link root")
	}

	outside := filepath.Join(directory, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "file.bin"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(realRoot, "linked")); err != nil {
		t.Fatal(err)
	}
	root, err := openInstallationRoot(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := rejectSymlinkComponents(root.root, filepath.Join("linked", "file.bin")); err == nil {
		t.Fatal("rejectSymlinkComponents accepted a symbolic-link component")
	}
	if _, _, _, err := root.openStableRegularFile("linked/file.bin"); err == nil {
		t.Fatal("rooted open traversed a symbolic-link component")
	}

	regular := filepath.Join(directory, "regular.bin")
	if err := os.WriteFile(regular, []byte("regular"), 0o600); err != nil {
		t.Fatal(err)
	}
	regularLink := filepath.Join(directory, "regular-link.bin")
	if err := os.Symlink(regular, regularLink); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openStableRegular(regularLink); err == nil {
		t.Fatal("openStableRegular accepted a symbolic link")
	}
}
