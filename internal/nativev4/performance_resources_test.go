package nativev4

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestBorrowedWindowReusesSessionAndWritesPayload(t *testing.T) {
	directory := t.TempDir()
	sourceData := make([]byte, 256<<10)
	targetData := make([]byte, len(sourceData))
	for i := range sourceData {
		sourceData[i] = byte(i*17 + i/31)
		targetData[i] = sourceData[i] ^ byte(i*29+3)
	}
	sourcePath := filepath.Join(directory, "source.bin")
	targetPath := filepath.Join(directory, "target.bin")
	outputPath := filepath.Join(directory, "payload.bin")
	if err := os.WriteFile(sourcePath, sourceData, 0o600); err != nil {
		t.Fatal(err)
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
	token := NewCancelToken(context.Background())
	defer token.Close()
	borrowed, err := session.BuildWindowBorrowed(token, uint64(len(sourceData)), uint64(len(targetData)), 0, uint32(len(targetData)), uint32(len(targetData)), 3, patchformat.OptimizeBalanced)
	if err != nil {
		t.Fatal(err)
	}
	if borrowed.Descriptor.PayloadSize == 0 {
		borrowed.Release()
		t.Fatal("expected a payload-bearing window")
	}
	if _, err := session.BuildWindowBorrowed(token, uint64(len(sourceData)), uint64(len(targetData)), 0, uint32(len(targetData)), uint32(len(targetData)), 3, patchformat.OptimizeBalanced); err == nil {
		borrowed.Release()
		t.Fatal("busy session accepted a second borrowed result")
	}
	if err := borrowed.WritePayloadAt(4096); err != nil {
		borrowed.Release()
		t.Fatal(err)
	}
	payloadSize := borrowed.Descriptor.PayloadSize
	borrowed.Release()
	info, err := output.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(4096+payloadSize) {
		t.Fatalf("payload file size = %d, want %d", info.Size(), 4096+payloadSize)
	}
	second, err := session.BuildWindowBorrowed(token, uint64(len(sourceData)), uint64(len(targetData)), 0, uint32(len(targetData)), uint32(len(targetData)), 3, patchformat.OptimizeBalanced)
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
}

func TestSessionPoolAndSharedCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.bin")
	data := make([]byte, 2<<20)
	for i := range data {
		data[i] = byte(i*7 + 11)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	pool, err := NewSessionPool(2, file, nil, nil, IOAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	first, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pool.Acquire(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("pool cancellation error = %v", err)
	}
	pool.Release(first)
	third, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pool.Release(third)
	pool.Release(second)

	ctx, stop := context.WithCancel(context.Background())
	stop()
	token := NewCancelToken(ctx)
	defer token.Close()
	session, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.HashFileWithToken(token, false, uint64(len(data)))
	pool.Release(session)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("shared cancellation error = %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Acquire(context.Background()); err == nil {
		t.Fatal("closed session pool accepted an acquisition")
	}
}
