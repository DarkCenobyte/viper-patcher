package creatorcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
)

func TestRunHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(stdout.String(), "Example:") || !strings.Contains(stdout.String(), "--file-pair") || !strings.Contains(stdout.String(), "<output.vipr>") {
		t.Fatalf("unexpected help: %s", stdout.String())
	}
}

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--version"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "viper-patcher creator") {
		t.Fatalf("code = %d, stdout = %s", code, stdout.String())
	}
}

func TestRunCreatesPatchWithAtomicPairs(t *testing.T) {
	root := t.TempDir()
	sourceOne := filepath.Join(root, "old", "one.bin")
	targetOne := filepath.Join(root, "new", "renamed-one.bin")
	sourceTwo := filepath.Join(root, "old", "nested", "two.bin")
	targetTwo := filepath.Join(root, "new", "renamed-two.bin")
	output := filepath.Join(root, "update.vipr")
	for path, content := range map[string]string{
		sourceOne: "old-one", targetOne: "new-one", sourceTwo: "old-two", targetTwo: "new-two",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Repeat(content, 2048)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--headless",
		"--file-pair", sourceOne + filePairSeparator + targetOne,
		"--file-pair", sourceTwo + filePairSeparator + targetTwo,
		"--create-reverse",
		output,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Patch created:") || !strings.Contains(stderr.String(), string("compressing-forward")) {
		t.Fatalf("stdout = %s, stderr = %s", stdout.String(), stderr.String())
	}
	parsed, _, err := patch.OpenWithDigest(output)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Header.Reverse || len(parsed.Header.Files) != 2 {
		t.Fatalf("unexpected header: %#v", parsed.Header)
	}
	if parsed.Header.Files[0].Path != "one.bin" || parsed.Header.Files[1].Path != "nested/two.bin" {
		t.Fatalf("unexpected paths: %#v", parsed.Header.Files)
	}
}

func TestRunArgumentValidation(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		want      string
	}{
		{name: "missing arguments", want: "Example:"},
		{name: "unknown parameter", arguments: []string{"--unknown"}, want: "flag provided but not defined"},
		{name: "removed source flag", arguments: []string{"--source-files", "one"}, want: "flag provided but not defined"},
		{name: "no pair", arguments: []string{"update.vipr"}, want: "at least one --file-pair"},
		{name: "malformed pair", arguments: []string{"--file-pair", "source-only", "update.vipr"}, want: "file pair must use the form"},
		{name: "empty source", arguments: []string{"--file-pair", "::target", "update.vipr"}, want: "file pair must use the form"},
		{name: "missing output", arguments: []string{"--file-pair", "one::two"}, want: "output .vipr path is required"},
		{name: "multiple outputs", arguments: []string{"--file-pair", "one::two", "first.vipr", "second.vipr"}, want: "expected exactly one output"},
		{name: "option after output", arguments: []string{"--file-pair", "one::two", "update.vipr", "--comment", "too late"}, want: "expected exactly one output"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := Run(context.Background(), test.arguments, &stdout, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("code = %d, stderr = %s, want %q", code, stderr.String(), test.want)
			}
		})
	}
}
