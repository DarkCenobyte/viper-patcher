package patchercli

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
	if code != 0 || !strings.Contains(stdout.String(), "--patch-file") {
		t.Fatalf("code = %d, stdout = %s", code, stdout.String())
	}
}

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--version"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "viper-patcher patcher") {
		t.Fatalf("code = %d, stdout = %s", code, stdout.String())
	}
}

func TestRunAppliesForwardAndReversePatch(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source")
	targetRoot := filepath.Join(root, "target")
	installRoot := filepath.Join(root, "install")
	for _, directory := range []string{sourceRoot, targetRoot, installRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(sourceRoot, "application.bin")
	target := filepath.Join(targetRoot, "renamed.bin")
	installed := filepath.Join(installRoot, "application.bin")
	oldData := []byte(strings.Repeat("old-data", 2048))
	newData := []byte(strings.Repeat("new-data", 2048))
	for path, data := range map[string][]byte{source: oldData, target: newData, installed: oldData} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	patchPath := filepath.Join(root, "update.vipr")
	if err := patch.Create(context.Background(), patch.CreateOptions{
		Files: []patch.FilePair{{SourcePath: source, TargetPath: target}}, OutputPath: patchPath,
		CompressionLevel: 3, CreateReverse: true,
	}, nil); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		extraArgs []string
		wantData  []byte
		message   string
	}{
		{name: "forward", wantData: newData, message: "forward patch applied successfully"},
		{name: "reverse", extraArgs: []string{"--reverse"}, wantData: oldData, message: "reverse patch applied successfully"},
	} {
		t.Run(test.name, func(t *testing.T) {
			arguments := []string{"--headless", "--patch-file", patchPath}
			arguments = append(arguments, test.extraArgs...)
			arguments = append(arguments, installRoot)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := Run(context.Background(), arguments, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("code = %d, stderr = %s", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.message) || !strings.Contains(stderr.String(), "Before:") || !strings.Contains(stderr.String(), "After:") {
				t.Fatalf("stdout = %s, stderr = %s", stdout.String(), stderr.String())
			}
			actual, err := os.ReadFile(installed)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, test.wantData) {
				t.Fatal("installed file content mismatch")
			}
		})
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

func TestRunRejectsExtraDirectory(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--patch-file", "update.vipr", "one", "two"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "exactly one target directory") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsUnknownParameter(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--unknown"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsInvalidPatch(t *testing.T) {
	root := t.TempDir()
	patchPath := filepath.Join(root, "invalid.vipr")
	if err := os.WriteFile(patchPath, []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--patch-file", patchPath, root}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "Patch validation failed") {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}
