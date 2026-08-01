package nativev4

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestApplyGroupReusesJustVerifiedSourceChunk(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.bin")
	outputPath := filepath.Join(directory, "output.bin")
	data := make([]byte, 1<<20)
	for index := range data {
		data[index] = byte(index*29 + index/31)
	}
	if err := os.WriteFile(sourcePath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()

	session, err := NewSession(source, nil, output)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.SetOutputSize(uint64(len(data))); err != nil {
		t.Fatal(err)
	}

	digest, err := HashBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	verification := NewSourceVerification([]patchformat.Digest{digest}, false)
	defer verification.Close()
	windows := []patchformat.WindowDescriptor{{
		OutputOffset:     0,
		OutputSize:       uint32(len(data)),
		Kind:             patchformat.WindowSame,
		Codec:            patchformat.CodecNone,
		SourceOffset:     0,
		SourceSize:       uint32(len(data)),
		SourceFirstChunk: 0,
		SourceChunkCount: 1,
		Digest:           digest,
	}}

	result, err := session.ApplyGroup(
		context.Background(),
		windows,
		0,
		uint32(len(data)),
		uint64(len(data)),
		verification,
		digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.BytesReadSource != uint64(len(data)) {
		t.Fatalf("source bytes read = %d, want %d", result.BytesReadSource, len(data))
	}
	if result.WindowsCompleted != 1 {
		t.Fatalf("completed windows = %d, want 1", result.WindowsCompleted)
	}
	if result.Flags&GroupResultDirectSame == 0 {
		t.Fatal("verified SAME group did not use the direct-write fast path")
	}
	if verification.States[0] != 2 {
		t.Fatalf("source verification state = %d, want verified", verification.States[0])
	}
	actual, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, data) {
		t.Fatal("output differs from verified source data")
	}
}

func TestApplyGroupPartiallyReusesPreferredVerifiedChunk(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.bin")
	outputPath := filepath.Join(directory, "output.bin")
	sourceData := make([]byte, 10<<20)
	for index := range sourceData {
		sourceData[index] = byte(index*37 + index/43)
	}
	const sourceOffset = 7 << 20
	const outputSize = 2 << 20
	expected := sourceData[sourceOffset : sourceOffset+outputSize]
	if err := os.WriteFile(sourcePath, sourceData, 0o600); err != nil {
		t.Fatal(err)
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	session, err := NewSession(source, nil, output)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.SetOutputSize(outputSize); err != nil {
		t.Fatal(err)
	}

	firstDigest, err := HashBytes(sourceData[:patchformat.IdentityChunkSize])
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := HashBytes(sourceData[patchformat.IdentityChunkSize:])
	if err != nil {
		t.Fatal(err)
	}
	outputDigest, err := HashBytes(expected)
	if err != nil {
		t.Fatal(err)
	}
	verification := NewSourceVerification([]patchformat.Digest{firstDigest, secondDigest}, false)
	defer verification.Close()
	windows := []patchformat.WindowDescriptor{{
		OutputOffset:     0,
		OutputSize:       outputSize,
		Kind:             patchformat.WindowCopy,
		Codec:            patchformat.CodecNone,
		SourceOffset:     sourceOffset,
		SourceSize:       outputSize,
		SourceFirstChunk: 0,
		SourceChunkCount: 2,
		Digest:           outputDigest,
	}}

	result, err := session.ApplyGroup(
		context.Background(),
		windows,
		0,
		outputSize,
		uint64(len(sourceData)),
		verification,
		outputDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Verification reads both canonical chunks (10 MiB). COPY then reuses the
	// final 1 MiB of the preferred first chunk and reads only its 1 MiB tail.
	const expectedSourceReads = 11 << 20
	if result.BytesReadSource != expectedSourceReads {
		t.Fatalf("source bytes read = %d, want %d", result.BytesReadSource, expectedSourceReads)
	}
	actual, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal("cross-chunk COPY output mismatch")
	}
}
