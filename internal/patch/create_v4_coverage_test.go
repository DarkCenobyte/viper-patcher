package patch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestV4CreateOptionValidation(t *testing.T) {
	valid := CreateOptions{
		Files:            []FilePair{{SourcePath: "source", TargetPath: "target"}},
		OutputPath:       "update.vipr",
		CompressionLevel: 3,
		Optimization:     OptimizeBalanced,
	}
	if err := validateCreateOptions(valid); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*CreateOptions){
		func(options *CreateOptions) { options.Files = nil },
		func(options *CreateOptions) { options.OutputPath = "" },
		func(options *CreateOptions) { options.CompressionLevel = -131073 },
		func(options *CreateOptions) { options.CompressionLevel = 23 },
		func(options *CreateOptions) { options.Optimization = OptimizationMode(255) },
		func(options *CreateOptions) { options.Files[0].SourcePath = "" },
		func(options *CreateOptions) { options.Files[0].TargetPath = "" },
	}
	for index, mutate := range mutations {
		options := valid
		options.Files = append([]FilePair(nil), valid.Files...)
		mutate(&options)
		if err := validateCreateOptions(options); err == nil {
			t.Fatalf("invalid mutation %d was accepted", index)
		}
	}
	if boolFlag(true) != 1 || boolFlag(false) != 0 {
		t.Fatal("boolFlag returned an invalid value")
	}
	if defaultWindowSize(0) != 1<<20 || defaultWindowSize(256<<10) != 256<<10 {
		t.Fatal("defaultWindowSize returned an invalid value")
	}
}

func TestV4EstimateCreate(t *testing.T) {
	if _, err := EstimateCreate(CreateOptions{}); err == nil {
		t.Fatal("EstimateCreate accepted no files")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "source.bin")
	target := filepath.Join(directory, "target.bin")
	if err := os.WriteFile(source, bytes.Repeat([]byte{1}, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, bytes.Repeat([]byte{2}, 200), 0o600); err != nil {
		t.Fatal(err)
	}
	estimate, err := EstimateCreate(CreateOptions{Files: []FilePair{{source, target}}, CreateReverse: true})
	if err != nil {
		t.Fatal(err)
	}
	if estimate.WorkDirectoryBytes != 300 || estimate.OutputDirectoryBytes <= 300 || estimate.TotalBytes != estimate.WorkDirectoryBytes+estimate.OutputDirectoryBytes {
		t.Fatalf("estimate = %+v", estimate)
	}
	if _, err := EstimateCreate(CreateOptions{Files: []FilePair{{filepath.Join(directory, "missing"), target}}}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source error = %v", err)
	}
	if _, err := EstimateCreate(CreateOptions{Files: []FilePair{{directory, target}}}); err == nil {
		t.Fatal("EstimateCreate accepted a directory input")
	}
}

func TestV4CreateReplacesExistingPatchAndCleansWork(t *testing.T) {
	directory := t.TempDir()
	workParent := filepath.Join(directory, "work")
	if err := os.Mkdir(workParent, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(directory, "source.bin")
	target := filepath.Join(directory, "target.bin")
	if err := os.WriteFile(source, bytes.Repeat([]byte("old"), 50<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, bytes.Repeat([]byte("new"), 50<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(directory, "nested", "update.vipr")
	options := CreateOptions{
		Files:            []FilePair{{source, target}},
		OutputPath:       output,
		CompressionLevel: 1,
		WorkDirectory:    workParent,
		WindowSize:       256 << 10,
		Optimization:     OptimizePatchSize,
	}
	if err := Create(context.Background(), options, nil); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, bytes.Repeat([]byte("changed"), 40<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Create(context.Background(), options, nil); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("recreated patch did not change")
	}
	if _, err := os.Stat(output + ".viper-backup"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup was not cleaned: %v", err)
	}
	entries, err := os.ReadDir(workParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("creator work directory contains %d entries", len(entries))
	}

	badWork := filepath.Join(directory, "work-file")
	if err := os.WriteFile(badWork, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	options.WorkDirectory = badWork
	options.OutputPath = filepath.Join(directory, "bad.vipr")
	if err := Create(context.Background(), options, nil); err == nil {
		t.Fatal("Create accepted a regular file as work-directory parent")
	}
}

func TestV4CreateCancellationAndWindowHelpers(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.bin")
	target := filepath.Join(directory, "snapshot.bin")
	if err := os.WriteFile(source, bytes.Repeat([]byte{0x5a}, 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := copySnapshot(ctx, source, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("copySnapshot cancellation error = %v", err)
	}
	if _, err := os.Stat(target); err == nil {
		_ = os.Remove(target)
	}

	windows, err := buildWindowSet(context.Background(), nil, nil, 0, 0, 256<<10, 1, OptimizeBalanced, 1)
	if err != nil || windows != nil {
		t.Fatalf("empty buildWindowSet = %#v, %v", windows, err)
	}

	output, err := os.Create(filepath.Join(directory, "payloads.bin"))
	if err != nil {
		t.Fatal(err)
	}
	built := []builtWindow{
		{descriptor: patchformat.WindowDescriptor{OutputSize: 1, Kind: patchformat.WindowZero}},
		{descriptor: patchformat.WindowDescriptor{OutputOffset: 1, OutputSize: 3, Kind: patchformat.WindowReplaceRaw, PayloadSize: 3}, payload: []byte("abc")},
	}
	if err := writeWindowPayloads(output, built); err != nil {
		output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if built[0].descriptor.PayloadOffset != 0 || built[1].descriptor.PayloadOffset != 0 {
		t.Fatalf("payload offsets = %d, %d", built[0].descriptor.PayloadOffset, built[1].descriptor.PayloadOffset)
	}
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "abc" {
		t.Fatalf("payload data = %q", data)
	}
	descriptors := descriptors(built)
	if len(descriptors) != 2 || descriptors[1].OutputSize != 3 {
		t.Fatalf("descriptors = %+v", descriptors)
	}
}
