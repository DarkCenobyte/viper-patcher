package nativev4

import (
	"context"
	"os"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func FuzzApplyMalformedDelta(f *testing.F) {
	f.Add([]byte{'V', '4', 'O', 'P', 'S', '\r', '\n', 1, patchformat.OpcodeEnd})
	f.Add([]byte{patchformat.OpcodeCopyAbsolute, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 1<<20 {
			t.Skip()
		}
		source, err := os.CreateTemp(t.TempDir(), "source-*")
		if err != nil {
			t.Fatal(err)
		}
		patch, err := os.CreateTemp(t.TempDir(), "patch-*")
		if err != nil {
			t.Fatal(err)
		}
		output, err := os.CreateTemp(t.TempDir(), "output-*")
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close()
		defer patch.Close()
		defer output.Close()
		if _, err := source.Write(make([]byte, 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := patch.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := output.Truncate(64); err != nil {
			t.Fatal(err)
		}
		session, err := NewSession(source, patch, output)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		window := patchformat.WindowDescriptor{
			OutputSize:       64,
			Kind:             patchformat.WindowDeltaRaw,
			Codec:            patchformat.CodecNone,
			PayloadSize:      uint32(len(payload)),
			ExpandedSize:     uint32(len(payload)),
			SourceSize:       64,
			SourceChunkCount: 1,
			InstructionCount: 1,
		}
		_, _ = session.ApplyChangedWindow(context.Background(), window, 64, nil)
	})
}
