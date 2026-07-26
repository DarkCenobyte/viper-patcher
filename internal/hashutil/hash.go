package hashutil

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

const Algorithm = "sha256"

// Reader returns the lowercase SHA-256 digest and byte count read from reader.
func Reader(reader io.Reader) (string, uint64, error) {
	hash := sha256.New()
	size, err := io.Copy(hash, reader)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), uint64(size), nil
}
