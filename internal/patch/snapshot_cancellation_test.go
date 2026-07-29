package patch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRegularFileRejectsCanceledContextBeforeCreatingOutput(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.bin")
	destinationPath := filepath.Join(directory, "snapshot.bin")
	if err := os.WriteFile(sourcePath, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := snapshotRegularFile(ctx, sourcePath, destinationPath, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if _, err := os.Stat(destinationPath); !os.IsNotExist(err) {
		t.Fatalf("canceled snapshot output exists: %v", err)
	}
}
