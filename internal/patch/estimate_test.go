//go:build ignore

package patch

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func TestEstimateHelpers(t *testing.T) {
	total, err := addEstimate(1, 2, 3)
	if err != nil || total != 6 {
		t.Fatalf("total = %d, err = %v", total, err)
	}
	if _, err := addEstimate(math.MaxUint64, 1); err == nil || !strings.Contains(err.Error(), "overflows") {
		t.Fatalf("overflow error = %v", err)
	}
	bound, err := compressionBoundEstimate(128)
	if err != nil || bound <= 128 {
		t.Fatalf("bound = %d, err = %v", bound, err)
	}
}

func TestRegularFileSizeRejectsMissingAndDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := regularFileSize(missing); err == nil {
		t.Fatal("missing file must fail")
	}
	if _, err := regularFileSize(t.TempDir()); err == nil {
		t.Fatal("directory must fail")
	}
}
