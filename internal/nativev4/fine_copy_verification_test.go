package nativev4

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestReferencedSameVerifiesExactWindowBeforeUse(t *testing.T) {
	const windowSize = 256 << 10
	sourceData := make([]byte, patchformat.IdentityChunkSize)
	for index := range sourceData {
		sourceData[index] = byte(index*31 + 7)
	}

	source, err := os.CreateTemp(t.TempDir(), "source-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.Write(sourceData); err != nil {
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
	exact, err := HashBytes(sourceData[:windowSize])
	if err != nil {
		t.Fatal(err)
	}

	session, err := NewSession(source, nil, output)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.SetOutputSize(windowSize); err != nil {
		t.Fatal(err)
	}

	window := patchformat.WindowDescriptor{
		OutputSize:       windowSize,
		Kind:             patchformat.WindowSame,
		SourceSize:       windowSize,
		SourceChunkCount: 1,
		Digest:           exact,
	}
	verification := NewSourceVerification([]patchformat.Digest{canonical}, false)
	defer verification.Close()

	result, err := session.ApplyGroup(
		context.Background(),
		[]patchformat.WindowDescriptor{window},
		0,
		windowSize,
		uint64(len(sourceData)),
		verification,
		exact,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.BytesReadSource != windowSize {
		t.Fatalf("source bytes read = %d, want %d", result.BytesReadSource, windowSize)
	}
	if verification.States[0] != 0 {
		t.Fatalf("canonical state = %d, want unverified", verification.States[0])
	}

	actual := make([]byte, windowSize)
	if _, err := output.ReadAt(actual, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, sourceData[:windowSize]) {
		t.Fatal("exactly verified SAME output differs from source")
	}
}
