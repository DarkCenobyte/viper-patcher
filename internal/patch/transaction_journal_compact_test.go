package patch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyJournalUsesCompactTransitionRecords(t *testing.T) {
	base := t.TempDir()
	root, err := openInstallationRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	name := applyJournalPrefix + "compact"
	file, err := root.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	entries := make([]applyJournalEntry, 64)
	for index := range entries {
		entries[index] = applyJournalEntry{
			Path:   fmt.Sprintf("nested/file-%02d.bin", index),
			Temp:   fmt.Sprintf("nested/.viper-v4-output-%02d", index),
			Backup: fmt.Sprintf("nested/.viper-v4-backup-%02d", index),
			State:  "prepared",
		}
	}
	journal := &applyJournal{
		root:       root,
		name:       name,
		durability: DurabilityBuffered,
		data: applyJournalData{
			Version: applyJournalVersion,
			State:   "prepared",
			Entries: entries,
		},
	}
	if err := journal.persist(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(base, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.mark(0, "backed-up"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(base, name))
	if err != nil {
		t.Fatal(err)
	}
	transitionBytes := after.Size() - before.Size()
	if transitionBytes <= 0 || transitionBytes >= before.Size()/8 {
		t.Fatalf("compact transition appended %d bytes after %d-byte snapshot", transitionBytes, before.Size())
	}
}

func TestRecoverCompactJournalIgnoresTruncatedTail(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "nested", "file.bin")
	backup := filepath.Join(base, "nested", ".viper-v4-backup-test")
	temp := filepath.Join(base, "nested", ".viper-v4-output-test")
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temp, []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}

	root, err := openInstallationRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	name := applyJournalPrefix + "truncated"
	file, err := root.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	journal := &applyJournal{
		root:       root,
		name:       name,
		durability: DurabilityBuffered,
		data: applyJournalData{
			Version: applyJournalVersion,
			State:   "prepared",
			Entries: []applyJournalEntry{{
				Path:   "nested/file.bin",
				Temp:   "nested/.viper-v4-output-test",
				Backup: "nested/.viper-v4-backup-test",
				State:  "prepared",
			}},
		},
	}
	if err := journal.persist(); err != nil {
		t.Fatal(err)
	}
	if err := journal.mark(0, "backed-up"); err != nil {
		t.Fatal(err)
	}
	file, err = root.root.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(`{"version":2,"kind":"entry"`)); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if err := recoverApplyTransactions(root, DurabilityBuffered); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != "original" {
		t.Fatalf("recovered data = %q", actual)
	}
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Fatalf("temporary output was not removed: %v", err)
	}
}

func TestRecoverLegacyApplyJournalSnapshot(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "nested", "file.bin")
	backup := filepath.Join(base, "nested", ".viper-v4-backup-legacy")
	temp := filepath.Join(base, "nested", ".viper-v4-output-legacy")
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temp, []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}

	root, err := openInstallationRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	name := applyJournalPrefix + "legacy"
	file, err := root.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(applyJournalRecord{
		Version: legacyApplyJournalVersion,
		State:   "committed",
		Entries: []applyJournalEntry{{
			Path:   "nested/file.bin",
			Temp:   "nested/.viper-v4-output-legacy",
			Backup: "nested/.viper-v4-backup-legacy",
			State:  "replaced",
		}},
	}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if err := recoverApplyTransactions(root, DurabilityBuffered); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != "replacement" {
		t.Fatalf("committed target changed to %q", actual)
	}
	for _, artifact := range []string{backup, temp} {
		if _, err := os.Stat(artifact); !os.IsNotExist(err) {
			t.Fatalf("legacy artifact was not removed: %s: %v", artifact, err)
		}
	}
}
