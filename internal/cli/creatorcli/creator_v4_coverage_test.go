package creatorcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func TestV4FilePairList(t *testing.T) {
	var pairs filePairList
	if pairs.String() != "" {
		t.Fatalf("empty pair list = %q", pairs.String())
	}
	if err := pairs.Set("old.bin::new.bin"); err != nil {
		t.Fatal(err)
	}
	if err := pairs.Set("nested/old.bin::nested/new.bin"); err != nil {
		t.Fatal(err)
	}
	if got := pairs.String(); got != "old.bin::new.bin, nested/old.bin::nested/new.bin" {
		t.Fatalf("pair list = %q", got)
	}
	for _, invalid := range []string{"source-only", "::target", "source::", " ::target", "source:: "} {
		if err := pairs.Set(invalid); err == nil {
			t.Fatalf("Set(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestV4CreatorRunHelpVersionAndValidation(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		wantCode  int
		wantOut   string
		wantErr   string
	}{
		{name: "missing arguments", wantCode: 2, wantErr: "Example:"},
		{name: "help", arguments: []string{"--help"}, wantCode: 0, wantOut: "--window-size"},
		{name: "version", arguments: []string{"--version"}, wantCode: 0, wantOut: "viper-patcher creator"},
		{name: "unknown flag", arguments: []string{"--unknown"}, wantCode: 2, wantErr: "flag provided but not defined"},
		{name: "missing pair", arguments: []string{"update.vipr"}, wantCode: 2, wantErr: "at least one --file-pair"},
		{name: "malformed pair", arguments: []string{"--file-pair", "source-only", "update.vipr"}, wantCode: 2, wantErr: "file pair must use the form"},
		{name: "missing output", arguments: []string{"--file-pair", "one::two"}, wantCode: 2, wantErr: "output .vipr path is required"},
		{name: "multiple outputs", arguments: []string{"--file-pair", "one::two", "first.vipr", "second.vipr"}, wantCode: 2, wantErr: "expected exactly one output"},
		{name: "empty output", arguments: []string{"--file-pair", "one::two", ""}, wantCode: 2, wantErr: "must not be empty"},
		{name: "invalid window", arguments: []string{"--file-pair", "one::two", "--window-size", "3M", "update.vipr"}, wantCode: 2, wantErr: "window size must be"},
		{name: "invalid optimization", arguments: []string{"--file-pair", "one::two", "--optimize", "latency", "update.vipr"}, wantCode: 2, wantErr: "unsupported optimization mode"},
		{name: "invalid compression", arguments: []string{"--file-pair", "one::two", "--compression-level", "23", "update.vipr"}, wantCode: 1, wantErr: "invalid zstd compression level"},
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

func TestV4CreatorRunCreatesPatch(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "old")
	targetRoot := filepath.Join(root, "new")
	for _, directory := range []string{sourceRoot, targetRoot} {
		if err := os.MkdirAll(filepath.Join(directory, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pairs := []struct {
		source, target   string
		oldData, newData []byte
	}{
		{filepath.Join(sourceRoot, "one.bin"), filepath.Join(targetRoot, "renamed-one.bin"), bytes.Repeat([]byte("old-one"), 4096), bytes.Repeat([]byte("new-one"), 4096)},
		{filepath.Join(sourceRoot, "nested", "two.bin"), filepath.Join(targetRoot, "nested", "renamed-two.bin"), bytes.Repeat([]byte{0x44}, 300<<10), bytes.Repeat([]byte{0x55}, 300<<10)},
	}
	for _, pair := range pairs {
		if err := os.WriteFile(pair.source, pair.oldData, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pair.target, pair.newData, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(root, "update.vipr")
	arguments := []string{
		"--headless",
		"--file-pair", pairs[0].source + filePairSeparator + pairs[0].target,
		"--file-pair", pairs[1].source + filePairSeparator + pairs[1].target,
		"--workers", "2",
		"--window-size", "256K",
		"--optimize", "apply-speed",
		"--create-reverse",
		output,
	}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Patch created:") || !strings.Contains(stderr.String(), string(progress.StageCompressingForward)) {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	parsed, digest, err := patch.OpenWithDigest(output)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || !parsed.Header.Reverse || len(parsed.Header.Files) != 2 {
		t.Fatalf("unexpected patch: digest=%q header=%+v", digest, parsed.Header)
	}
	if parsed.Header.DefaultWindowSize != 256<<10 || parsed.Header.Files[0].Path != "one.bin" || parsed.Header.Files[1].Path != "nested/two.bin" {
		t.Fatalf("unexpected V4 paths/window size: %+v", parsed.Header)
	}
}

func TestV4CreatorTerminalProgress(t *testing.T) {
	var output bytes.Buffer
	reporter := newTerminalProgress(&output)
	reporter.Report(progress.Event{Stage: progress.StagePreparing})
	reporter.Report(progress.Event{FileIndex: 1, FileCount: 2, Path: "a.bin", Stage: progress.StageSnapshotting, ProcessedBytes: 5, TotalBytes: 10, Overall: 0.25})
	reporter.Report(progress.Event{FileIndex: 1, FileCount: 2, Path: "a.bin", Stage: progress.StageSnapshotting, ProcessedBytes: 10, TotalBytes: 10, Overall: 0.5})
	reporter.Report(progress.Event{Stage: progress.StageCompleted})
	reporter.Finish()
	text := output.String()
	for _, want := range []string{"preparing", "[1/2] snapshotting: a.bin", "Progress:", "50.00%", "100.00%"} {
		if !strings.Contains(text, want) {
			t.Fatalf("progress output %q does not contain %q", text, want)
		}
	}
	if reporter.lineActive {
		t.Fatal("progress line remains active")
	}
}
