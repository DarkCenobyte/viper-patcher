package patchergui

import (
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestDirectionsAfterApply(t *testing.T) {
	parsed := patchformat.Patch{Header: patchformat.Header{
		Reverse: true,
		Files: []patchformat.FileEntry{{
			SourceHash: "source",
			TargetHash: "target",
			SourceSize: 1,
			TargetSize: 2,
		}},
	}}
	if forward, reverse := directionsAfterApply(parsed, patch.Forward); forward || !reverse {
		t.Fatalf("after forward = %v/%v, want false/true", forward, reverse)
	}
	if forward, reverse := directionsAfterApply(parsed, patch.Reverse); !forward || reverse {
		t.Fatalf("after reverse = %v/%v, want true/false", forward, reverse)
	}

	parsed.Header.Files[0].TargetHash = parsed.Header.Files[0].SourceHash
	parsed.Header.Files[0].TargetSize = parsed.Header.Files[0].SourceSize
	if forward, reverse := directionsAfterApply(parsed, patch.Forward); !forward || !reverse {
		t.Fatalf("identical states after forward = %v/%v, want true/true", forward, reverse)
	}
}
