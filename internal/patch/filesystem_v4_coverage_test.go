package patch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestV4StableRegularAndInstallationRootHelpers(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "regular.bin")
	if err := os.WriteFile(path, []byte("stable"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, identity, err := openStableRegular(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := stableUnchanged(file, path, identity); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if wrapOperationError("close", path, nil) != nil {
		file.Close()
		t.Fatal("nil operation error was wrapped")
	}
	cause := errors.New("cause")
	wrapped := wrapOperationError("read", path, cause)
	if !errors.Is(wrapped, cause) || !strings.Contains(wrapped.Error(), "read") {
		file.Close()
		t.Fatalf("wrapped error = %v", wrapped)
	}
	changed := identity.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(path, changed, changed); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := stableUnchanged(file, path, identity); err == nil {
		file.Close()
		t.Fatal("stableUnchanged accepted a modified path")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openStableRegular(filepath.Join(directory, "missing.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing regular file error = %v", err)
	}
	if _, _, err := openStableRegular(directory); err == nil {
		t.Fatal("openStableRegular accepted a directory")
	}

	if root := (*installationRoot)(nil); root.Close() != nil {
		t.Fatal("nil installation root close failed")
	}
	if _, err := openInstallationRoot(filepath.Join(directory, "missing-root")); err == nil {
		t.Fatal("openInstallationRoot accepted a missing root")
	}
	rootFile := filepath.Join(directory, "root-file")
	if err := os.WriteFile(rootFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openInstallationRoot(rootFile); err == nil {
		t.Fatal("openInstallationRoot accepted a regular file")
	}

	root, err := openInstallationRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if localized, err := localPatchPath("regular.bin"); err != nil || localized != "regular.bin" {
		t.Fatalf("localPatchPath = %q, %v", localized, err)
	}
	for _, unsafe := range []string{".", "../escape", "/absolute"} {
		if _, err := localPatchPath(unsafe); err == nil {
			t.Fatalf("localPatchPath(%q) unexpectedly succeeded", unsafe)
		}
	}
	opened, openedIdentity, name, err := root.openStableRegularFile("regular.bin")
	if err != nil {
		t.Fatal(err)
	}
	if name != "regular.bin" {
		opened.Close()
		t.Fatalf("localized root name = %q", name)
	}
	if err := stableRootUnchanged(root, opened, name, openedIdentity); err != nil {
		opened.Close()
		t.Fatal(err)
	}
	if err := stableRootUnchanged(nil, opened, name, openedIdentity); err == nil {
		opened.Close()
		t.Fatal("stableRootUnchanged accepted a nil root")
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := root.openStableRegularFile("missing.bin"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing rooted file error = %v", err)
	}
	if _, _, _, err := root.openStableRegularFile("."); err == nil {
		t.Fatal("rooted open accepted an unsafe path")
	}
	if err := rejectSymlinkComponents(root.root, filepath.Join("missing", "child")); err != nil {
		t.Fatalf("missing path components should be allowed: %v", err)
	}

	temp, tempName, err := createRootTemp(root.root, "", ".viper-test-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := temp.Write([]byte("temporary")); err != nil {
		temp.Close()
		t.Fatal(err)
	}
	if err := temp.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := root.root.Stat(tempName); err != nil {
		t.Fatal(err)
	}
	if err := root.root.Remove(tempName); err != nil {
		t.Fatal(err)
	}
	reserved, err := reserveRootPath(root, ".", ".viper-reserved-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.root.Stat(reserved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reserved path still exists: %v", err)
	}
}

func TestV4CommitPreparedBufferedAndDurable(t *testing.T) {
	for _, durability := range []DurabilityMode{DurabilityBuffered, DurabilityDurable} {
		t.Run(string(durability), func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Mkdir(filepath.Join(directory, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, item := range []struct {
				path string
				old  string
				new  string
			}{
				{path: "one.bin", old: "one-old", new: "one-new"},
				{path: filepath.Join("nested", "two.bin"), old: "two-old", new: "two-new"},
			} {
				if err := os.WriteFile(filepath.Join(directory, item.path), []byte(item.old), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			root, err := openInstallationRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			var prepared []preparedFile
			for _, item := range []struct {
				path string
				new  string
			}{
				{path: "one.bin", new: "one-new"},
				{path: filepath.Join("nested", "two.bin"), new: "two-new"},
			} {
				source, identity, name, err := root.openStableRegularFile(filepath.ToSlash(item.path))
				if err != nil {
					t.Fatal(err)
				}
				temp, tempName, err := createRootTemp(root.root, filepath.Dir(name), ".viper-output-")
				if err != nil {
					source.Close()
					t.Fatal(err)
				}
				if _, err := temp.Write([]byte(item.new)); err != nil {
					source.Close()
					temp.Close()
					t.Fatal(err)
				}
				if durability == DurabilityDurable {
					if err := temp.Sync(); err != nil {
						source.Close()
						temp.Close()
						t.Fatal(err)
					}
				}
				if err := temp.Close(); err != nil {
					source.Close()
					t.Fatal(err)
				}
				prepared = append(prepared, preparedFile{path: name, temp: tempName, source: source, identity: identity})
			}
			if err := commitPrepared(root, prepared, durability); err != nil {
				t.Fatal(err)
			}
			for path, want := range map[string]string{"one.bin": "one-new", filepath.Join("nested", "two.bin"): "two-new"} {
				got, err := os.ReadFile(filepath.Join(directory, path))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != want {
					t.Fatalf("%s = %q, want %q", path, got, want)
				}
			}
		})
	}
}
