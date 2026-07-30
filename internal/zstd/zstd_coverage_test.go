//go:build vipr_legacy_zstd

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
	inputPath := filepath.Join(directory, "input.bin")
	compressedPath := filepath.Join(directory, "payload.zst")
	inputData := []byte(strings.Repeat("callback-data-", 8192))
	if err := os.WriteFile(inputPath, inputData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CompressFile(inputPath, compressedPath, 3, nil); err != nil {
		t.Fatal(err)
	}
	return compressedPath, inputData
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
	if err := (&Decoder{}).DecompressSegmentToFile(context.Background(), nil, 0, 0, nil, 0, nil, nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("unexpected closed decoder error: %v", err)
	}

	decoder, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	if err := decoder.DecompressSegmentToFile(context.Background(), nil, 0, 0, nil, 0, nil, nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("unexpected nil file error: %v", err)
	}
	if err := decoder.DecompressSegmentToWriter(context.Background(), nil, 0, 0, nil, 0, nil); err == nil || !strings.Contains(err.Error(), "writer") {
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
	compressedPath, inputData := coverageStandaloneFrame(t)
	compressed, err := os.Open(compressedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	info, err := compressed.Stat()
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
			context.Background(),
			compressed,
			0,
			uint64(info.Size()),
			output,
			uint64(len(inputData)),
			func(value, total uint64) { processed = value },
			func(block []byte) error {
				callbackData = append(callbackData, block...)
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(callbackData, inputData) {
			t.Fatal("output callback did not receive the decompressed data")
		}
		if processed != uint64(len(inputData)) {
			t.Fatalf("processed = %d", processed)
		}
	})

	t.Run("writer", func(t *testing.T) {
		var output bytes.Buffer
		err := decoder.DecompressSegmentToWriter(
			context.Background(), compressed, 0, uint64(info.Size()), &output, uint64(len(inputData)), nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(output.Bytes(), inputData) {
			t.Fatal("writer did not receive the decompressed data")
		}
	})

	t.Run("output callback error", func(t *testing.T) {
		output := coverageOutputFile(t)
		sentinel := errors.New("stop output")
		err := decoder.DecompressSegmentToFile(
			context.Background(), compressed, 0, uint64(info.Size()), output, uint64(len(inputData)), nil,
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
		err := decoder.DecompressSegmentToFile(ctx, compressed, 0, uint64(info.Size()), output, uint64(len(inputData)), nil, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})
}
