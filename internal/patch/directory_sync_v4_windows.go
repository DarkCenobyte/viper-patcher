//go:build windows

package patch

// Windows has no portable directory-handle durability primitive equivalent to
// fsync on a directory. Durable mode still flushes each prepared output file.
func syncDirectoryV4(*installationRoot, string) error { return nil }
