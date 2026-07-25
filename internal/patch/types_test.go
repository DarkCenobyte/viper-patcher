package patch

import (
	"strings"
	"testing"
)

func TestValidationResultError(t *testing.T) {
	tests := []struct {
		name   string
		result ValidationResult
		want   string
	}{
		{name: "ready", result: ValidationResult{State: StateForwardReady}},
		{name: "missing fallback", result: ValidationResult{State: StateMissingFiles}, want: "required by the patch"},
		{name: "missing details", result: ValidationResult{State: StateMissingFiles, Missing: []string{"one.bin"}, Mismatched: []HashMismatch{{Path: "two.bin"}}}, want: "2 file(s) have a problem"},
		{name: "mismatch fallback", result: ValidationResult{State: StateHashMismatch}, want: "file hashes"},
		{name: "mismatch details", result: ValidationResult{State: StateHashMismatch, Mismatched: []HashMismatch{{Path: "one.bin"}}}, want: "first mismatched file: one.bin"},
		{name: "unknown", result: ValidationResult{State: "unknown"}, want: "unknown validation state"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.result.Error()
			if test.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidationResultReady(t *testing.T) {
	if !(ValidationResult{State: StateForwardReady}).Ready(Forward) {
		t.Fatal("forward state should be ready")
	}
	if !(ValidationResult{State: StateReverseReady}).Ready(Reverse) {
		t.Fatal("reverse state should be ready")
	}
	if (ValidationResult{State: StateForwardReady}).Ready(Reverse) {
		t.Fatal("forward state must not be reverse-ready")
	}
}
