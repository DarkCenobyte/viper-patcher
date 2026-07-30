package patch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestV4CreatorStreamsManyWindowsInPayloadOrder(t *testing.T) {
	directory := t.TempDir()
	source := make([]byte, 4<<20)
	target := make([]byte, len(source))
	for i := range source {
		source[i] = byte(i*13 + i/97)
		target[i] = source[i]
	}
	for offset := 0; offset < len(target); offset += 256 << 10 {
		end := min(offset+(64<<10), len(target))
		for i := offset; i < end; i++ {
			target[i] ^= byte(i*19 + 5)
		}
	}
	sourcePath := filepath.Join(directory, "source.bin")
	targetPath := filepath.Join(directory, "target.bin")
	patchPath := filepath.Join(directory, "streamed.vipr")
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, target, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Create(context.Background(), CreateOptions{
		Files:            []FilePair{{SourcePath: sourcePath, TargetPath: targetPath}},
		OutputPath:       patchPath,
		CompressionLevel: 3,
		WorkerBudget:     3,
		WindowSize:       256 << 10,
		Optimization:     OptimizeBalanced,
	}, nil); err != nil {
		t.Fatal(err)
	}
	parsed, err := Open(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	windows := parsed.Header.Files[0].ForwardWindows
	if len(windows) != 16 {
		t.Fatalf("window count = %d, want 16", len(windows))
	}
	var next uint64 = 16
	for index, window := range windows {
		if window.PayloadSize == 0 {
			continue
		}
		if window.PayloadOffset != next {
			t.Fatalf("window %d payload offset = %d, want %d", index, window.PayloadOffset, next)
		}
		next += uint64(window.PayloadSize)
	}
}

func TestV4PreallocationPolicy(t *testing.T) {
	for _, test := range []struct {
		durability DurabilityMode
		profile    IOProfile
		want       bool
	}{
		{DurabilityDurable, IOSSD, true},
		{DurabilityDurable, IONVMe, true},
		{DurabilityDurable, IOHDD, false},
		{DurabilityDurable, IOAuto, false},
		{DurabilityBuffered, IOSSD, false},
	} {
		if got := shouldPreallocateOutput(test.durability, test.profile); got != test.want {
			t.Fatalf("shouldPreallocateOutput(%q, %q) = %v, want %v", test.durability, test.profile, got, test.want)
		}
	}
}

func TestV4CreatorWindowPipelineHonorsCancellation(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.bin")
	targetPath := filepath.Join(directory, "target.bin")
	outputPath := filepath.Join(directory, "payloads.bin")
	data := make([]byte, 1<<20)
	if err := os.WriteFile(sourcePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := make(chan error, 1)
	go func() {
		_, err := buildWindowSetToOutput(ctx, output, source, target, uint64(len(data)), uint64(len(data)), 256<<10, 1, OptimizeBalanced, 3)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled window pipeline succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled window pipeline did not drain its workers")
	}
}

func TestV4CreatorWindowPipelineDrainsAfterOutputFailure(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.bin")
	targetPath := filepath.Join(directory, "target.bin")
	outputPath := filepath.Join(directory, "payloads.bin")
	sourceData := make([]byte, 2<<20)
	targetData := make([]byte, len(sourceData))
	for i := range sourceData {
		sourceData[i] = byte(i*17 + i/31)
		targetData[i] = sourceData[i] ^ 0x5a
	}
	if err := os.WriteFile(sourcePath, sourceData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, targetData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	output, err := os.Open(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	result := make(chan error, 1)
	go func() {
		_, err := buildWindowSetToOutput(context.Background(), output, source, target, uint64(len(sourceData)), uint64(len(targetData)), 256<<10, 1, OptimizeBalanced, 4)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("read-only output unexpectedly accepted payload writes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("failed window pipeline did not release pending borrowed windows")
	}
}
