package patch

import "fmt"

// Direction identifies which differential direction to apply.
type Direction string

const (
	Forward Direction = "forward"
	Reverse Direction = "reverse"
)

// ValidationState describes the files currently present in a target directory.
type ValidationState string

const (
	StateForwardReady ValidationState = "forward-ready"
	StateReverseReady ValidationState = "reverse-ready"
	StateMissingFiles ValidationState = "missing-files"
	StateHashMismatch ValidationState = "hash-mismatch"
)

// HashMismatch records one existing file whose digest is not expected.
type HashMismatch struct {
	Path   string
	Actual string
}

// ValidationResult describes whether a patch can be applied or reversed.
type ValidationResult struct {
	State      ValidationState
	Missing    []string
	Mismatched []HashMismatch
}

func (result ValidationResult) Ready(direction Direction) bool {
	return (direction == Forward && result.State == StateForwardReady) ||
		(direction == Reverse && result.State == StateReverseReady)
}

func (result ValidationResult) Error() error {
	switch result.State {
	case StateForwardReady, StateReverseReady:
		return nil
	case StateMissingFiles:
		if len(result.Missing) == 0 {
			return fmt.Errorf("one or more files required by the patch are missing")
		}
		problemCount := len(result.Missing) + len(result.Mismatched)
		return fmt.Errorf("%d file(s) have a problem (%d missing); first missing file: %s", problemCount, len(result.Missing), result.Missing[0])
	case StateHashMismatch:
		if len(result.Mismatched) == 0 {
			return fmt.Errorf("one or more file hashes do not match the patch")
		}
		return fmt.Errorf("%d file hash(es) do not match; first mismatched file: %s", len(result.Mismatched), result.Mismatched[0].Path)
	default:
		return fmt.Errorf("unknown validation state %q", result.State)
	}
}
