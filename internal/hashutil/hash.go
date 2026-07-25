package hashutil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

const Algorithm = "sha256"

// File returns the lowercase SHA-256 digest and size of path.
func File(path string) (string, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open %q: %w", path, err)
	}
	defer file.Close()

	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash %q: %w", path, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), uint64(size), nil
}

// Reader returns the lowercase SHA-256 digest and byte count read from reader.
func Reader(reader io.Reader) (string, uint64, error) {
	hash := sha256.New()
	size, err := io.Copy(hash, reader)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), uint64(size), nil
}
