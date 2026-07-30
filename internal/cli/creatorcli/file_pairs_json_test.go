package creatorcli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFilePairsJSON(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pairs.json")
	content := `[
		{"source":"old/a.bin","target":"new/a.bin"},
		{"source":"old/b.bin","target":"new/b.bin"}
	]`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	pairs, err := loadFilePairsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 {
		t.Fatalf("pair count = %d", len(pairs))
	}
	if pairs[0].SourcePath != filepath.Join(root, "old", "a.bin") {
		t.Fatalf("source path = %q", pairs[0].SourcePath)
	}
	if pairs[1].TargetPath != filepath.Join(root, "new", "b.bin") {
		t.Fatalf("target path = %q", pairs[1].TargetPath)
	}
}

func TestLoadFilePairsJSONRejectsInvalidInput(t *testing.T) {
	root := t.TempDir()
	tests := map[string]string{
		"empty":        `[]`,
		"missing":      `[{"source":"a"}]`,
		"unknown":      `[{"source":"a","target":"b","extra":true}]`,
		"two-values":   `[{"source":"a","target":"b"}] []`,
		"not-an-array": `{"source":"a","target":"b"}`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name+".json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadFilePairsJSON(path); err == nil {
				t.Fatal("invalid JSON unexpectedly accepted")
			}
		})
	}
}
