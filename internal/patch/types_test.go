//go:build ignore

package patch

import (
	"errors"
	"strings"
	"testing"
)

func TestValidationResultReady(t *testing.T) {
	result := ValidationResult{CanApplyForward: true, CanApplyReverse: true}
	if !result.Ready(Forward) || !result.Ready(Reverse) {
		t.Fatalf("both directions should be ready: %#v", result)
	}
	if result.Ready(Direction("unknown")) {
		t.Fatal("unknown direction must not be ready")
	}
}

func TestValidationResultErrorFor(t *testing.T) {
	tests := []struct {
		name      string
		result    ValidationResult
		direction Direction
		want      string
	}{
		{
			name:      "ready",
			result:    ValidationResult{State: StateForwardReady, CanApplyForward: true},
			direction: Forward,
		},
		{
			name:      "opposite direction",
			result:    ValidationResult{State: StateForwardReady, CanApplyForward: true},
			direction: Reverse,
			want:      "opposite patch direction",
		},
		{
			name:      "missing fallback",
			result:    ValidationResult{State: StateMissingFiles},
			direction: Forward,
			want:      "required files are missing",
		},
		{
			name: "missing details",
			result: ValidationResult{
				State:   StateMissingFiles,
				Missing: []string{"one.bin"},
				Issues:  []FileIssue{{Path: "two.bin", Reason: IssueHashMismatch}},
			},
			direction: Forward,
			want:      "2 file(s) have a problem",
		},
		{
			name:      "mixed",
			result:    ValidationResult{State: StateMixedFiles},
			direction: Forward,
			want:      "mixture of source and target",
		},
		{
			name:      "invalid fallback",
			result:    ValidationResult{State: StateInvalidFiles},
			direction: Forward,
			want:      "do not match",
		},
		{
			name: "invalid details",
			result: ValidationResult{
				State:  StateInvalidFiles,
				Issues: []FileIssue{{Path: "one.bin", Reason: IssueNotRegular}},
			},
			direction: Forward,
			want:      "one.bin (not-regular)",
		},
		{
			name:      "unknown",
			result:    ValidationResult{State: ValidationState("unknown")},
			direction: Forward,
			want:      "unknown validation state",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.result.ErrorFor(test.direction)
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

func TestCommittedWarningHelpers(t *testing.T) {
	var nilWarning *CommittedWarning
	if nilWarning.Error() != "" || nilWarning.Unwrap() != nil {
		t.Fatal("nil committed warning must be empty")
	}

	causeOne := errors.New("cleanup one")
	causeTwo := errors.New("cleanup two")
	withoutOperation := &CommittedWarning{Err: causeOne}
	if !strings.Contains(withoutOperation.Error(), "operation committed") || !errors.Is(withoutOperation, causeOne) {
		t.Fatalf("unexpected warning: %v", withoutOperation)
	}
	withOperation := committedWarning("patch creation", causeOne, nil, &CommittedWarning{Err: causeTwo})
	if !IsCommittedWarning(withOperation) || !errors.Is(withOperation, causeOne) || !errors.Is(withOperation, causeTwo) {
		t.Fatalf("unexpected joined warning: %v", withOperation)
	}
	if committedWarning("unused", nil) != nil || IsCommittedWarning(errors.New("ordinary failure")) {
		t.Fatal("warning classification is incorrect")
	}
}
