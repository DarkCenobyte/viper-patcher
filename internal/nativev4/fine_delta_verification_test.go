package nativev4

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestReferencedDeltaUsesFineBandAndConsumesVerifiedBytes(t *testing.T) {
	const (
		fineSize   = 64 << 10
		sourceSize = patchformat.IdentityChunkSize
		sourceBase = 1 << 20
	)
	sourceData := make([]byte, sourceSize)
	for index := range sourceData {
		sourceData[index] = byte(index*17 + 11)
	}
	expected := sourceData[sourceBase : sourceBase+fineSize]

	source, err := os.CreateTemp(t.TempDir(), "source-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.Write(sourceData); err != nil {
		t.Fatal(err)
	}

	payload := []byte{'V', '4', 'O', 'P', 'S', '\r', '\n', 1, patchformat.OpcodeCopyLocalLong}
	payload = binary.AppendUvarint(payload, 0)
	payload = binary.AppendUvarint(payload, fineSize)
	payload = append(payload, patchformat.OpcodeEnd)
	patchFile, err := os.CreateTemp(t.TempDir(), "patch-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer patchFile.Close()
	if _, err := patchFile.Write(payload); err != nil {
		t.Fatal(err)
	}

	output, err := os.CreateTemp(t.TempDir(), "output-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()

	canonical, err := HashBytes(sourceData)
	if err != nil {
		t.Fatal(err)
	}
	fineDigest, err := HashBytes(expected)
	if err != nil {
		t.Fatal(err)
	}

	session, err := NewSession(source, patchFile, output)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.SetOutputSize(fineSize); err != nil {
		t.Fatal(err)
	}

	window := patchformat.WindowDescriptor{
		OutputSize:       fineSize,
		Kind:             patchformat.WindowDeltaRaw,
		Codec:            patchformat.CodecNone,
		PayloadSize:      uint32(len(payload)),
		ExpandedSize:     uint32(len(payload)),
		SourceOffset:     sourceBase,
		SourceSize:       fineSize,
		SourceChunkCount: 1,
		InstructionCount: 1,
		Digest:           fineDigest,
	}
	verification := NewSourceVerificationWithFine(
		[]patchformat.Digest{canonical},
		fineSize,
		[]patchformat.FineDigest{{Index: sourceBase / fineSize, Digest: fineDigest}},
		false,
	)
	defer verification.Close()

	result, err := session.ApplyGroup(
		context.Background(),
		[]patchformat.WindowDescriptor{window},
		0,
		fineSize,
		sourceSize,
		verification,
		fineDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.BytesReadSource != fineSize {
		t.Fatalf("source bytes read = %d, want %d", result.BytesReadSource, fineSize)
	}
	if verification.States[0] != 0 || verification.FineStates[0] != 2 {
		t.Fatalf("canonical/fine states = %d/%d, want 0/2", verification.States[0], verification.FineStates[0])
	}

	actual := make([]byte, fineSize)
	if _, err := output.ReadAt(actual, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatal("fine-verified delta output differs from source")
	}

	wrong := fineDigest
	wrong[0] ^= 0xff
	badVerification := NewSourceVerificationWithFine(
		[]patchformat.Digest{canonical},
		fineSize,
		[]patchformat.FineDigest{{Index: sourceBase / fineSize, Digest: wrong}},
		false,
	)
	defer badVerification.Close()
	if _, err := session.ApplyGroup(
		context.Background(),
		[]patchformat.WindowDescriptor{window},
		0,
		fineSize,
		sourceSize,
		badVerification,
		fineDigest,
	); !IsSourceMismatch(err) {
		t.Fatalf("fine digest mismatch error = %v", err)
	}
}
