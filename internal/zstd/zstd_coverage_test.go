package zstd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func coverageStandaloneFrame(t *testing.T) (string, []byte) {
	t.Helper()
	directory := t.TempDir()
	referencePath := filepath.Join(directory, "reference.bin")
	targetPath := filepath.Join(directory, "target.bin")
	patchPath := filepath.Join(directory, "patch.zst")
	targetData := []byte(strings.Repeat("callback-data-", 8192))
	if err := os.WriteFile(referencePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, targetData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CompressFile(referencePath, targetPath, patchPath, 3, nil); err != nil {
		t.Fatal(err)
	}
	return patchPath, targetData
}

func coverageOutputFile(t *testing.T) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "output.bin")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestDecoderValidationAndCloseCoverage(t *testing.T) {
	var nilDecoder *Decoder
	if err := nilDecoder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (&Decoder{}).DecompressSegmentToFile(context.Background(), nil, nil, 0, 0, nil, 0, nil, nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("unexpected closed decoder error: %v", err)
	}

	decoder, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	if err := decoder.DecompressSegmentToFile(context.Background(), nil, nil, 0, 0, nil, 0, nil, nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("unexpected nil file error: %v", err)
	}
	if err := decoder.DecompressSegmentToWriter(context.Background(), nil, nil, 0, 0, nil, 0, nil); err == nil || !strings.Contains(err.Error(), "writer") {
		t.Fatalf("unexpected nil writer error: %v", err)
	}
	if err := decoder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDecoderCallbacksAndCancellationCoverage(t *testing.T) {
	patchPath, targetData := coverageStandaloneFrame(t)
	patch, err := os.Open(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	defer patch.Close()
	info, err := patch.Stat()
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()

	t.Run("success", func(t *testing.T) {
		output := coverageOutputFile(t)
		var callbackData []byte
		var processed uint64
		err := decoder.DecompressSegmentToFile(
			nil,
			nil,
			patch,
			0,
			uint64(info.Size()),
			output,
			uint64(len(targetData)),
			func(value, total uint64) { processed = value },
			func(block []byte) error {
				callbackData = append(callbackData, block...)
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(callbackData, targetData) {
			t.Fatal("output callback did not receive the decompressed data")
		}
		if processed != uint64(len(targetData)) {
			t.Fatalf("processed = %d", processed)
		}
	})

	t.Run("writer", func(t *testing.T) {
		var output bytes.Buffer
		err := decoder.DecompressSegmentToWriter(
			context.Background(), nil, patch, 0, uint64(info.Size()), &output, uint64(len(targetData)), nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(output.Bytes(), targetData) {
			t.Fatal("writer did not receive the decompressed data")
		}
	})

	t.Run("output callback error", func(t *testing.T) {
		output := coverageOutputFile(t)
		sentinel := errors.New("stop output")
		err := decoder.DecompressSegmentToFile(
			context.Background(), nil, patch, 0, uint64(info.Size()), output, uint64(len(targetData)), nil,
			func([]byte) error { return sentinel },
		)
		if !errors.Is(err, sentinel) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		output := coverageOutputFile(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := decoder.DecompressSegmentToFile(ctx, nil, patch, 0, uint64(info.Size()), output, uint64(len(targetData)), nil, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})
}
