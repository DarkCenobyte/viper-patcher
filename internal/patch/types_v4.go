package patch

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

type Direction string

const (
	Forward Direction = "forward"
	Reverse Direction = "reverse"
)

type FilePair struct{ SourcePath, TargetPath string }

type OptimizationMode = patchformat.OptimizationMode

const (
	OptimizeBalanced   = patchformat.OptimizeBalanced
	OptimizeApplySpeed = patchformat.OptimizeApplySpeed
	OptimizePatchSize  = patchformat.OptimizePatchSize
)

type VerifyMode string

const (
	VerifyReferenced VerifyMode = "referenced"
	VerifyStrict     VerifyMode = "strict"
	VerifyOutput     VerifyMode = "output"
)

func ParseVerifyMode(value string) (VerifyMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "referenced":
		return VerifyReferenced, nil
	case "strict":
		return VerifyStrict, nil
	case "output":
		return VerifyOutput, nil
	default:
		return "", fmt.Errorf("unsupported verification mode %q", value)
	}
}

type DurabilityMode string

const (
	DurabilityBuffered DurabilityMode = "buffered"
	DurabilityDurable  DurabilityMode = "durable"
)

func ParseDurabilityMode(value string) (DurabilityMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "buffered":
		return DurabilityBuffered, nil
	case "durable":
		return DurabilityDurable, nil
	default:
		return "", fmt.Errorf("unsupported durability mode %q", value)
	}
}

type IOProfile string

const (
	IOAuto IOProfile = "auto"
	IOHDD  IOProfile = "hdd"
	IOSSD  IOProfile = "ssd"
	IONVMe IOProfile = "nvme"
)

func ParseIOProfile(value string) (IOProfile, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return IOAuto, nil
	case "hdd":
		return IOHDD, nil
	case "ssd":
		return IOSSD, nil
	case "nvme":
		return IONVMe, nil
	default:
		return "", fmt.Errorf("unsupported I/O profile %q", value)
	}
}

func ParseWindowSize(value string) (uint32, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if normalized == "" || normalized == "AUTO" {
		return 0, nil
	}
	multiplier := uint64(1)
	if strings.HasSuffix(normalized, "K") {
		multiplier = 1 << 10
		normalized = strings.TrimSuffix(normalized, "K")
	} else if strings.HasSuffix(normalized, "M") {
		multiplier = 1 << 20
		normalized = strings.TrimSuffix(normalized, "M")
	}
	number, err := strconv.ParseUint(normalized, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid window size %q", value)
	}
	size := number * multiplier
	switch size {
	case 256 << 10, 512 << 10, 1 << 20, 2 << 20, 4 << 20, 8 << 20:
		return uint32(size), nil
	default:
		return 0, fmt.Errorf("window size must be auto, 256K, 512K, 1M, 2M, 4M, or 8M")
	}
}

func automaticWindowSize(size uint64) uint32 {
	switch {
	case size < 1<<20:
		return 256 << 10
	case size <= 16<<20:
		return 512 << 10
	case size <= 128<<20:
		return 1 << 20
	case size <= 1<<30:
		return 2 << 20
	default:
		return 4 << 20
	}
}
func max64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

type CommittedWarning struct {
	Operation string
	Err       error
}

func (w *CommittedWarning) Error() string {
	if w == nil {
		return ""
	}
	return fmt.Sprintf("%s committed with a cleanup warning: %v", w.Operation, w.Err)
}
func (w *CommittedWarning) Unwrap() error {
	if w == nil {
		return nil
	}
	return w.Err
}
func IsCommittedWarning(err error) bool {
	var warning *CommittedWarning
	return errors.As(err, &warning)
}
func committedWarning(operation string, causes ...error) error {
	var filtered []error
	for _, err := range causes {
		if err != nil {
			filtered = append(filtered, err)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return &CommittedWarning{operation, errors.Join(filtered...)}
}

type ValidationState string

const (
	StateForwardReady       ValidationState = "forward-ready"
	StateReverseReady       ValidationState = "reverse-ready"
	StateBidirectionalReady ValidationState = "bidirectional-ready"
	StateMissingFiles       ValidationState = "missing-files"
	StateMixedFiles         ValidationState = "mixed-files"
	StateInvalidFiles       ValidationState = "invalid-files"
)

type IssueReason string

const (
	IssueHashMismatch IssueReason = "hash-mismatch"
	IssueNotRegular   IssueReason = "not-regular"
)

type FileIssue struct {
	Path   string
	Reason IssueReason
}
type ValidationResult struct {
	State                            ValidationState
	CanApplyForward, CanApplyReverse bool
	Missing                          []string
	Issues                           []FileIssue
}

func (r ValidationResult) Ready(direction Direction) bool {
	if direction == Forward {
		return r.CanApplyForward
	}
	if direction == Reverse {
		return r.CanApplyReverse
	}
	return false
}
func (r ValidationResult) ErrorFor(direction Direction) error {
	if r.Ready(direction) {
		return nil
	}
	return fmt.Errorf("%s patch cannot be applied: validation state %s", direction, r.State)
}
