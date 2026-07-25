package creatorgui

import "testing"

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
	if !model.Remove(0) || model.Len() != 1 || model.Pairs()[0].SourcePath != "old/two.bin" {
		t.Fatalf("unexpected model after removal: %#v", model.Pairs())
	}
	model.Clear()
	if model.Len() != 0 {
		t.Fatalf("length = %d", model.Len())
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
