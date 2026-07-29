package nativev4

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestApplyGroupUsesCanonicalWindowDescriptors(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.bin")
	outputPath := filepath.Join(directory, "output.bin")
	data := bytes.Repeat([]byte{0x5a}, 3<<20)
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

	const windowSize = 256 << 10
	windows := make([]patchformat.WindowDescriptor, len(data)/windowSize)
	for index := range windows {
		offset := uint64(index * windowSize)
		windows[index] = patchformat.WindowDescriptor{
			OutputOffset: offset,
			OutputSize:   windowSize,
			Kind:         patchformat.WindowSame,
			Codec:        patchformat.CodecNone,
			SourceOffset: offset,
			SourceSize:   windowSize,
		}
	}
	digest, err := HashBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ApplyGroup(
		context.Background(),
		windows,
		0,
		uint32(len(data)),
		uint64(len(data)),
		nil,
		digest,
	); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, data) {
		t.Fatal("native V4 group output mismatch")
	}
}

func TestApplyChangedWindowUsesCanonicalDescriptor(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "output.bin")
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	session, err := NewSession(nil, nil, output)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	const outputSize = 256 << 10
	if err := session.SetOutputSize(outputSize); err != nil {
		t.Fatal(err)
	}
	expected := make([]byte, outputSize)
	digest, err := HashBytes(expected)
	if err != nil {
		t.Fatal(err)
	}
	window := patchformat.WindowDescriptor{
		OutputSize: outputSize,
		Kind:       patchformat.WindowZero,
		Codec:      patchformat.CodecNone,
		Digest:     digest,
	}
	if _, err := session.ApplyChangedWindow(context.Background(), window, 0, nil); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal("native V4 changed-window output mismatch")
	}
}
