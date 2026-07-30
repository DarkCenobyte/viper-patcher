package nativev4

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestSourceVerificationLoadSource(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "source-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data := bytes.Repeat([]byte("cache-data"), 8192)
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	digest, err := HashBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	verification := NewSourceVerification([]patchformat.Digest{digest}, false)
	defer verification.Close()
	if err := verification.LoadSource(context.Background(), file, uint64(len(data)), true); err != nil {
		t.Fatal(err)
	}
	if verification.source == nil || verification.sourceSize != uint64(len(data)) || verification.States[0] != 2 {
		t.Fatalf("cache was not initialized: %+v", verification)
	}
}
