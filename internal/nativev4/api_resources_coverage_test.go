package nativev4

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestNativeResourcePublicWrappers(t *testing.T) {
	if ZstdVersion() == "" || BLAKE3Backend() == "" {
		t.Fatal("native backend versions are empty")
	}
	nativeErr := &NativeError{Status: StatusSourceMismatch, Detail: "mismatch"}
	if !strings.Contains(nativeErr.Error(), "mismatch") || !IsSourceMismatch(nativeErr) || IsUnsupported(nativeErr) {
		t.Fatalf("native error helpers failed: %v", nativeErr)
	}
	unsupported := &NativeError{Status: StatusUnsupported}
	if !IsUnsupported(unsupported) || !strings.Contains(unsupported.Error(), "unsupported") {
		t.Fatalf("unsupported helper failed: %v", unsupported)
	}

	directory := t.TempDir()
	data := bytes.Repeat([]byte("hash-and-build-window"), 20<<10)
	sourcePath := filepath.Join(directory, "source.bin")
	targetPath := filepath.Join(directory, "target.bin")
	outputPath := filepath.Join(directory, "output.bin")
	if err := os.WriteFile(sourcePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	targetData := append([]byte(nil), data...)
	for i := 32 << 10; i < 48<<10; i++ {
		targetData[i] ^= 0x5a
	}
	if err := os.WriteFile(targetPath, targetData, 0o600); err != nil {
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
	session, err := NewSession(source, target, output)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	standard, err := session.HashFile(context.Background(), false, uint64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := HashBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if standard != expected {
		t.Fatalf("standard hash = %s, want %s", standard.Hex(), expected.Hex())
	}
	root, chunks, err := session.HashFileTree(context.Background(), false, uint64(len(data)), patchformat.IdentityChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := TreeRoot(uint64(len(data)), patchformat.IdentityChunkSize, chunks)
	if err != nil || rebuilt != root {
		t.Fatalf("tree root = %s, %v; want %s", rebuilt.Hex(), err, root.Hex())
	}
	built, err := session.BuildWindow(context.Background(), uint64(len(data)), uint64(len(targetData)), 0, 256<<10, 256<<10, 3, patchformat.OptimizeBalanced)
	if err != nil {
		t.Fatal(err)
	}
	if built.Descriptor.OutputSize != 256<<10 {
		t.Fatalf("built window = %+v", built.Descriptor)
	}
	verification := NewSourceVerification(chunks, true)
	for index, state := range verification.States {
		if state != 2 {
			t.Fatalf("verification state %d = %d", index, state)
		}
	}
	if err := session.SetOutputSize(uint64(len(data)), false); err != nil {
		t.Fatal(err)
	}
	if err := session.FlushOutput(); err != nil {
		t.Fatal(err)
	}
	cloneErr := session.CloneOutput(uint64(len(data)))
	if cloneErr != nil && !IsUnsupported(cloneErr) {
		t.Fatal(cloneErr)
	}
}

func TestNativeResourceValidation(t *testing.T) {
	if _, err := NewSessionPool(0, nil, nil, nil, IOAuto); err == nil {
		t.Fatal("zero-sized session pool succeeded")
	}
	var session *Session
	if _, err := session.HashFile(context.Background(), false, 0); err == nil {
		t.Fatal("nil session hash succeeded")
	}
	if err := session.SetOutputSize(0); err == nil {
		t.Fatal("nil session resize succeeded")
	}
	if err := session.FlushOutput(); err == nil {
		t.Fatal("nil session flush succeeded")
	}
	if err := session.CloneOutput(0); err == nil {
		t.Fatal("nil session clone succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pool, err := NewSessionPool(1, nil, nil, nil, IOAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	first, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire = %v", err)
	}
	pool.Release(first)
}
