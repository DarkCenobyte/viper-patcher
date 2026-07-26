package creatorgui

import (
	"path/filepath"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
)

func TestFilePairModelKeepsAssociations(t *testing.T) {
	model := &filePairModel{}
	if err := model.Add("old/one.bin", "new/renamed-one.bin"); err != nil {
		t.Fatal(err)
	}
	if err := model.Add("old/two.bin", "new/renamed-two.bin"); err != nil {
		t.Fatal(err)
	}
	pairs := model.Pairs()
	if len(pairs) != 2 || pairs[0].SourcePath != "old/one.bin" || pairs[0].TargetPath != "new/renamed-one.bin" {
		t.Fatalf("unexpected pairs: %#v", pairs)
	}
	pairs[0].SourcePath = "mutated"
	if model.Pairs()[0].SourcePath != "old/one.bin" {
		t.Fatal("Pairs must return an independent snapshot")
	}
	if !model.Remove(0) || len(model.Pairs()) != 1 || model.Pairs()[0].SourcePath != "old/two.bin" {
		t.Fatalf("unexpected model after removal: %#v", model.Pairs())
	}
	model.Clear()
	if pairs := model.Pairs(); len(pairs) != 0 {
		t.Fatalf("unexpected pairs after clear: %#v", pairs)
	}
}

func TestFilePairModelRejectsIncompletePair(t *testing.T) {
	model := &filePairModel{}
	if err := model.Add("", "target"); err == nil {
		t.Fatal("expected incomplete pair to be rejected")
	}
	if err := model.Add("source", ""); err == nil {
		t.Fatal("expected incomplete pair to be rejected")
	}
}

func TestFilePairDisplayUsesColumnRelativePaths(t *testing.T) {
	root := t.TempDir()
	pairs := []patch.FilePair{
		{SourcePath: filepath.Join(root, "source", "1.txt"), TargetPath: filepath.Join(root, "target", "1.txt")},
		{SourcePath: filepath.Join(root, "source", "2.txt"), TargetPath: filepath.Join(root, "target", "2.txt")},
		{SourcePath: filepath.Join(root, "source", "a", "3.txt"), TargetPath: filepath.Join(root, "target", "renamed", "3.txt")},
	}
	display := buildFilePairDisplay(pairs)
	if len(display) != 3 {
		t.Fatalf("display length = %d", len(display))
	}
	if display[0].Source != "1.txt" || display[1].Source != "2.txt" || display[2].Source != "a/3.txt" {
		t.Fatalf("unexpected source display: %#v", display)
	}
	if display[0].Target != "1.txt" || display[1].Target != "2.txt" || display[2].Target != "renamed/3.txt" {
		t.Fatalf("unexpected target display: %#v", display)
	}
}

func TestFilePairDisplayUsesBasenameForSinglePair(t *testing.T) {
	root := t.TempDir()
	display := buildFilePairDisplay([]patch.FilePair{{
		SourcePath: filepath.Join(root, "old", "source.bin"),
		TargetPath: filepath.Join(root, "new", "target.bin"),
	}})
	if len(display) != 1 || display[0].Source != "source.bin" || display[0].Target != "target.bin" {
		t.Fatalf("unexpected display: %#v", display)
	}
}
