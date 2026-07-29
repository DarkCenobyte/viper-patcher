//go:build ignore

package patch

import "testing"

func TestInstallationRootHelperBoundaries(t *testing.T) {
	var root *installationRoot
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".", "../escape", "/absolute"} {
		if _, err := localPatchPath(path); err == nil {
			t.Fatalf("expected %q to be rejected", path)
		}
	}
	localized, err := localPatchPath("data/file.bin")
	if err != nil || localized == "" {
		t.Fatalf("localized path = %q, err = %v", localized, err)
	}
}
