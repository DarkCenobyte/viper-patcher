package patch

import (
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
)

func TestChunkedReplaceThresholdRequiresTwoIdentityChunks(t *testing.T) {
	if got, want := chunkedReplaceThreshold, 2*hashutil.ChunkSize; got != want {
		t.Fatalf("chunked replace threshold = %d, want %d", got, want)
	}
}
