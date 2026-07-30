//go:build !windows

package patch

import (
	"io/fs"
	"testing"
)

func TestValidateInstalledMetadataRejectsPrivilegeBits(t *testing.T) {
	for _, bit := range []fs.FileMode{fs.ModeSetuid, fs.ModeSetgid, fs.ModeSticky} {
		if err := validateInstalledMetadata(0o755 | bit); err == nil {
			t.Fatalf("mode %v unexpectedly accepted", bit)
		}
	}
	if err := validateInstalledMetadata(0o640); err != nil {
		t.Fatal(err)
	}
	if got := targetPermissions(0o751); got != 0o751 {
		t.Fatalf("permissions = %v", got)
	}
}
