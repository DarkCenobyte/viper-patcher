package patchformat

import "testing"

func TestFormatThreeAcceptsChunkedReplaceWithBLAKE3(t *testing.T) {
	header := validHeader()
	header.Files[0].ForwardMethod = MethodChunkedReplace
	header.Files[0].TargetSize = 1
	if err := ValidateHeader(header); err != nil {
		t.Fatal(err)
	}
}

func TestFormatThreeRequiresExplicitMethods(t *testing.T) {
	header := validHeader()
	header.Files[0].ForwardMethod = ""
	if err := ValidateHeader(header); err == nil {
		t.Fatal("missing forward method must be rejected")
	}
}
