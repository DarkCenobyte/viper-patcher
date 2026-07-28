package workerbudget

import "runtime"

// Maximum returns the largest accepted explicit worker target.
func Maximum() int {
	workers := runtime.NumCPU()
	if workers < 1 {
		return 1
	}
	return workers
}

// Automatic returns the process-aware automatic worker target. GOMAXPROCS
// reflects container CPU quotas when the Go runtime can detect them.
func Automatic() int {
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if maximum := Maximum(); workers > maximum {
		workers = maximum
	}
	return workers
}

// Effective resolves 0 or a negative internal value to Automatic and caps an
// explicit value defensively. User-facing validation should reject negatives.
func Effective(value int) int {
	if value <= 0 {
		return Automatic()
	}
	if maximum := Maximum(); value > maximum {
		return maximum
	}
	return value
}

// IsValid reports whether a user-supplied target is automatic (0) or an
// explicit value within the supported logical CPU range.
func IsValid(value int) bool {
	return value >= 0 && value <= Maximum()
}
