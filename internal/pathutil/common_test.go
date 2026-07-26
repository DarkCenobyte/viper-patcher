package pathutil

import (
	"path/filepath"
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

func TestCaseInsensitiveKeyNormalizesUnicodeAndCase(t *testing.T) {
	composed := CaseInsensitiveKey("Data/É.TXT")
	decomposed := CaseInsensitiveKey("data/e\u0301.txt")
	if composed != decomposed {
		t.Fatalf("keys differ: %q != %q", composed, decomposed)
	}
	if composed == CaseInsensitiveKey("data/other.txt") {
		t.Fatal("different paths must not share a collision key")
	}
}
