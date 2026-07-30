//go:build vipr_legacy_zstd

package zstd

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestDecoderRejectsWindowAboveDefaultLimit(t *testing.T) {
	// This is a valid empty zstd frame whose window descriptor requests
	// 144 MiB: 128 MiB base plus one 16 MiB mantissa step.
	frame := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x89, 0x01, 0x00, 0x00}
	patch, err := os.CreateTemp(t.TempDir(), "oversized-window-*.zst")
	if err != nil {
		t.Fatal(err)
	}
	defer patch.Close()
	if _, err := patch.Write(frame); err != nil {
		t.Fatal(err)
	}

	decoder, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()

	var output bytes.Buffer
	err = decoder.DecompressSegmentToWriter(
		context.Background(),
		patch,
		0,
		uint64(len(frame)),
		&output,
		144<<20,
		nil,
	)
	if err == nil {
		t.Fatal("oversized zstd window was accepted")
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "decompress payload") || !strings.Contains(message, "memory") {
		t.Fatalf("unexpected oversized-window error: %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("decoder produced %d bytes before rejecting the oversized window", output.Len())
	}
}
