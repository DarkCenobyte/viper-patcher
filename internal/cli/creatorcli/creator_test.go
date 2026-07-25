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
	if !strings.Contains(stdout.String(), "Example:") || !strings.Contains(stdout.String(), "--source-files") || !strings.Contains(stdout.String(), "<output.vipr>") {
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

func TestRunCreatesPatch(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "old.bin")
	target := filepath.Join(root, "new.bin")
	output := filepath.Join(root, "update.vipr")
	if err := os.WriteFile(source, []byte(strings.Repeat("old", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(strings.Repeat("new", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"--headless",
		"--source-files", source,
		"--target-files", target,
		"--create-reverse",
		output,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Patch created:") || !strings.Contains(stderr.String(), "compressing-forward") {
		t.Fatalf("stdout = %s, stderr = %s", stdout.String(), stderr.String())
	}
	parsed, err := patch.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Header.Reverse || parsed.Header.Comment != "Created with Viper-Patcher" {
		t.Fatalf("unexpected header: %#v", parsed.Header)
	}
}

func TestRunRejectsMissingArguments(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), nil, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "Example:") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsUnknownParameter(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--unknown"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "flag provided but not defined") || !strings.Contains(stderr.String(), "Supported parameters and arguments:") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsRemovedOutputFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	arguments := []string{"--source-files", "one", "--target-files", "two", "--output", "update.vipr"}
	code := Run(context.Background(), arguments, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "flag provided but not defined: -output") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsMissingOutputPath(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	arguments := []string{"--source-files", "one", "--target-files", "two"}
	code := Run(context.Background(), arguments, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "output .vipr path is required as the final argument") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsMultipleOutputPaths(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	arguments := []string{"--source-files", "one", "--target-files", "two", "first.vipr", "second.vipr"}
	code := Run(context.Background(), arguments, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "expected exactly one output .vipr path") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsOptionAfterOutputPath(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	arguments := []string{"--source-files", "one", "--target-files", "two", "update.vipr", "--comment", "too late"}
	code := Run(context.Background(), arguments, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "expected exactly one output .vipr path") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsEmptyRepeatedValue(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--source-files="}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "file path must not be empty") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsUnequalLists(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	arguments := []string{"--source-files", "one", "--target-files", "two", "--target-files", "three", "x.vipr"}
	code := Run(context.Background(), arguments, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "source-files contains 1") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}
