package patch

import "fmt"

// Direction identifies which differential direction to apply.
type Direction string

const (
	Forward Direction = "forward"
	Reverse Direction = "reverse"
)

// FilePair keeps one source file permanently associated with its target file.
type FilePair struct {
	SourcePath string
	TargetPath string
}

// ValidationState describes the files currently present in a target directory.
type ValidationState string

const (
	StateForwardReady       ValidationState = "forward-ready"
	StateReverseReady       ValidationState = "reverse-ready"
	StateBidirectionalReady ValidationState = "bidirectional-ready"
	StateMissingFiles       ValidationState = "missing-files"
	StateMixedFiles         ValidationState = "mixed-files"
	StateInvalidFiles       ValidationState = "invalid-files"
)

// IssueReason identifies why an existing file cannot be used in either direction.
type IssueReason string

const (
	IssueHashMismatch IssueReason = "hash-mismatch"
	IssueModeMismatch IssueReason = "mode-mismatch"
	IssueNotRegular   IssueReason = "not-regular"
)

// FileIssue records one existing file that does not match an expected state.
type FileIssue struct {
	Path       string
	Reason     IssueReason
	ActualHash string
	ActualMode uint32
}

// ValidationResult describes which patch directions can be applied safely.
type ValidationResult struct {
	State           ValidationState
	CanApplyForward bool
	CanApplyReverse bool
	Missing         []string
	Issues          []FileIssue
}

// Ready reports whether direction can be applied to the inspected directory.
func (result ValidationResult) Ready(direction Direction) bool {
	switch direction {
	case Forward:
		return result.CanApplyForward
	case Reverse:
		return result.CanApplyReverse
	default:
		return false
	}
}

// ErrorFor describes why direction cannot be applied safely.
func (result ValidationResult) ErrorFor(direction Direction) error {
	if result.Ready(direction) {
		return nil
	}
	prefix := fmt.Sprintf("%s patch cannot be applied", direction)
	switch result.State {
	case StateMissingFiles:
		if len(result.Missing) == 0 {
			return fmt.Errorf("%s: one or more required files are missing", prefix)
		}
		problemCount := len(result.Missing) + len(result.Issues)
		return fmt.Errorf("%s: %d file(s) have a problem (%d missing); first missing file: %s", prefix, problemCount, len(result.Missing), result.Missing[0])
	case StateMixedFiles:
		return fmt.Errorf("%s: the target directory contains a mixture of source and target file states", prefix)
	case StateInvalidFiles:
		if len(result.Issues) == 0 {
			return fmt.Errorf("%s: one or more files do not match the patch", prefix)
		}
		return fmt.Errorf("%s: %d file(s) do not match; first affected file: %s (%s)", prefix, len(result.Issues), result.Issues[0].Path, result.Issues[0].Reason)
	case StateForwardReady, StateReverseReady, StateBidirectionalReady:
		return fmt.Errorf("%s: the installed files match only the opposite patch direction", prefix)
	default:
		return fmt.Errorf("%s: unknown validation state %q", prefix, result.State)
	}
}
