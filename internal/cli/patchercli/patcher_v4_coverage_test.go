package patchercli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
	"github.com/DarkCenobyte/viper-patcher/internal/workerbudget"
)

func TestV4PatcherRunHelpVersionAndValidation(t *testing.T) {
	invalidWorkers := strconv.Itoa(workerbudget.Maximum() + 1)
	tests := []struct {
		name      string
		arguments []string
		wantCode  int
		wantOut   string
		wantErr   string
	}{
		{name: "missing arguments", wantCode: 2, wantErr: "Example:"},
		{name: "help", arguments: []string{"--help"}, wantCode: 0, wantOut: "--verify"},
		{name: "version", arguments: []string{"--version"}, wantCode: 0, wantOut: "viper-patcher patcher"},
		{name: "unknown flag", arguments: []string{"--unknown"}, wantCode: 2, wantErr: "flag provided but not defined"},
		{name: "missing patch", arguments: []string{"root"}, wantCode: 2, wantErr: "--patch-file"},
		{name: "missing root", arguments: []string{"--patch-file", "update.vipr"}, wantCode: 2, wantErr: "exactly one target directory"},
		{name: "extra root", arguments: []string{"--patch-file", "update.vipr", "one", "two"}, wantCode: 2, wantErr: "exactly one target directory"},
		{name: "invalid workers", arguments: []string{"--patch-file", "update.vipr", "--workers", invalidWorkers, "root"}, wantCode: 2, wantErr: "--workers must be"},
		{name: "invalid verify", arguments: []string{"--patch-file", "update.vipr", "--verify", "none", "root"}, wantCode: 2, wantErr: "unsupported verification mode"},
		{name: "invalid durability", arguments: []string{"--patch-file", "update.vipr", "--durability", "sync-all", "root"}, wantCode: 2, wantErr: "unsupported durability mode"},
		{name: "invalid profile", arguments: []string{"--patch-file", "update.vipr", "--io-profile", "tape", "root"}, wantCode: 2, wantErr: "unsupported I/O profile"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), test.arguments, &stdout, &stderr)
			if code != test.wantCode {
				t.Fatalf("code = %d, want %d; stdout=%q stderr=%q", code, test.wantCode, stdout.String(), stderr.String())
			}
			if test.wantOut != "" && !strings.Contains(stdout.String(), test.wantOut) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.wantOut)
			}
			if test.wantErr != "" && !strings.Contains(stderr.String(), test.wantErr) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantErr)
			}
		})
	}
}

func TestV4PatcherRunAppliesForwardAndReverse(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source", "application.bin")
	targetPath := filepath.Join(root, "target", "renamed.bin")
	installRoot := filepath.Join(root, "install")
	for _, directory := range []string{filepath.Dir(sourcePath), filepath.Dir(targetPath), installRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldData := bytes.Repeat([]byte("old-data-"), 40<<10)
	newData := append([]byte(nil), oldData...)
	for index := 32 << 10; index < 96<<10; index++ {
		newData[index] ^= byte(index*17 + 3)
	}
	for path, data := range map[string][]byte{
		sourcePath: oldData,
		targetPath: newData,
		filepath.Join(installRoot, "application.bin"): oldData,
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	patchPath := filepath.Join(root, "update.vipr")
	if err := patch.Create(context.Background(), patch.CreateOptions{
		Files:            []patch.FilePair{{SourcePath: sourcePath, TargetPath: targetPath}},
		OutputPath:       patchPath,
		CompressionLevel: 3,
		CreateReverse:    true,
		WindowSize:       256 << 10,
		Optimization:     patch.OptimizeBalanced,
	}, nil); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		extra     []string
		wantData  []byte
		wantLabel string
	}{
		{name: "forward", extra: []string{"--verify", "output", "--durability", "durable", "--io-profile", "ssd"}, wantData: newData, wantLabel: "forward patch applied successfully"},
		{name: "reverse", extra: []string{"--reverse", "--verify", "strict", "--durability", "buffered", "--io-profile", "hdd"}, wantData: oldData, wantLabel: "reverse patch applied successfully"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments := []string{"--headless", "--patch-file", patchPath, "--workers", "2"}
			arguments = append(arguments, test.extra...)
			arguments = append(arguments, installRoot)
			var stdout, stderr bytes.Buffer
			if code := Run(context.Background(), arguments, &stdout, &stderr); code != 0 {
				t.Fatalf("code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), test.wantLabel) || !strings.Contains(stderr.String(), "Before:") || !strings.Contains(stderr.String(), "Committed:") {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			actual, err := os.ReadFile(filepath.Join(installRoot, "application.bin"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, test.wantData) {
				t.Fatal("installed file content mismatch")
			}
		})
	}
}

func TestV4PatcherRunRejectsInvalidPatch(t *testing.T) {
	root := t.TempDir()
	patchPath := filepath.Join(root, "invalid.vipr")
	if err := os.WriteFile(patchPath, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--patch-file", patchPath, root}, &stdout, &stderr); code != 1 {
		t.Fatalf("code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Patch application failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestV4PatcherTerminalProgress(t *testing.T) {
	var output bytes.Buffer
	reporter := newTerminalProgress(&output)
	reporter.Report(progress.Event{FileIndex: 1, FileCount: 2, Path: "a.bin", Stage: progress.StageApplying, ProcessedBytes: 5, TotalBytes: 10, Overall: 0.2})
	reporter.Report(progress.Event{FileIndex: 1, FileCount: 2, Path: "a.bin", Stage: progress.StageVerifying, ProcessedBytes: 10, TotalBytes: 10, Overall: 0.4})
	reporter.Report(progress.Event{FileIndex: 1, FileCount: 2, Path: "a.bin", Stage: progress.StageFilePrepared})
	reporter.Report(progress.Event{FileIndex: 1, FileCount: 2, Path: "a.bin", Stage: progress.StageFileCompleted})
	reporter.Report(progress.Event{FileIndex: 1, FileCount: 2, Path: "a.bin", Stage: progress.StageFileCompleted})
	reporter.Report(progress.Event{Stage: progress.StageCompleted})
	reporter.Finish()
	text := output.String()
	for _, want := range []string{"Before: a.bin", "Applying:", "Verifying:", "Prepared: a.bin", "Committed: a.bin"} {
		if !strings.Contains(text, want) {
			t.Fatalf("progress output %q does not contain %q", text, want)
		}
	}
	if strings.Count(text, "Committed: a.bin") != 1 {
		t.Fatalf("duplicate commit line in %q", text)
	}
}
