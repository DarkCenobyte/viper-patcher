package nativedialog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizeExtensions(t *testing.T) {
	actual := normalizeExtensions([]string{"vipr", ".png", ""})
	expected := []string{".vipr", ".png"}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("extensions = %#v", actual)
	}
}

func TestInitialDirectoryUsesFileParent(t *testing.T) {
	path := filepath.Join("root", "folder", "update.vipr")
	if actual := initialDirectory(path); actual != filepath.Join("root", "folder") {
		t.Fatalf("directory = %q", actual)
	}
}

func TestInitialDirectoryKeepsExistingDirectory(t *testing.T) {
	directory := t.TempDir()
	if actual := initialDirectory(directory); actual != directory {
		t.Fatalf("directory = %q", actual)
	}
}

func TestInitialDirectoryUsesExistingFileParentWithoutExtension(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "LICENSE")
	if err := os.WriteFile(path, []byte("license"), 0o600); err != nil {
		t.Fatal(err)
	}
	if actual := initialDirectory(path); actual != directory {
		t.Fatalf("directory = %q", actual)
	}
}
