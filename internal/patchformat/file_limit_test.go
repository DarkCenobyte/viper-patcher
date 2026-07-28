package patchformat

import (
	"strings"
	"testing"
)

func TestDecodeBoundedFileEntriesRejectsLimitBeforeValidation(t *testing.T) {
	if _, err := decodeBoundedFileEntries([]byte(`[{}, {}, {}]`), 2); err == nil || !strings.Contains(err.Error(), "more than 2 files") {
		t.Fatalf("unexpected limit error: %v", err)
	}
}

func TestDecodeBoundedFileEntriesKeepsStrictFields(t *testing.T) {
	if _, err := decodeBoundedFileEntries([]byte(`[{"unexpected":true}]`), 2); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unexpected unknown-field error: %v", err)
	}
}
