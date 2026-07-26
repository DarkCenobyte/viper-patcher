package pathutil

import (
	"path/filepath"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// CaseInsensitiveKey returns a Unicode-normalized, case-folded key suitable for
// conservative collision detection across case-insensitive filesystems.
func CaseInsensitiveKey(value string) string {
	normalized := norm.NFC.String(filepath.ToSlash(filepath.Clean(value)))
	return norm.NFC.String(cases.Fold().String(normalized))
}
