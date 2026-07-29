package hashutil

import (
	"context"
	"fmt"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/nativev4"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

const ChunkSize uint64 = patchformat.IdentityChunkSize

func File(ctx context.Context, file *os.File, size uint64) (string, uint64, error) {
	session, err := nativev4.NewSession(file, nil, nil)
	if err != nil {
		return "", 0, err
	}
	defer session.Close()
	root, _, err := session.HashFileTree(ctx, false, size, ChunkSize)
	return root.Hex(), size, err
}
func FileWithChunks(ctx context.Context, file *os.File, size uint64) (string, []patchformat.Digest, error) {
	session, err := nativev4.NewSession(file, nil, nil)
	if err != nil {
		return "", nil, err
	}
	defer session.Close()
	root, chunks, err := session.HashFileTree(ctx, false, size, ChunkSize)
	return root.Hex(), chunks, err
}
func Bytes(data []byte) ([32]byte, error) {
	value, err := nativev4.HashBytes(data)
	return [32]byte(value), err
}
func Root(size uint64, chunks []patchformat.Digest) (string, error) {
	value, err := nativev4.TreeRoot(size, ChunkSize, chunks)
	if err != nil {
		return "", fmt.Errorf("build BLAKE3 tree root: %w", err)
	}
	return value.Hex(), nil
}
