package patch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateJournalEntry(t *testing.T) {
	valid := applyJournalEntry{
		Path:   "nested/file.bin",
		Temp:   "nested/.viper-v4-output-abc",
		Backup: "nested/.viper-v4-backup-abc",
	}
	if err := validateJournalEntry(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Backup = "../outside"
	if err := validateJournalEntry(invalid); err == nil {
		t.Fatal("unsafe journal entry accepted")
	}
}

func TestRecoverApplyJournalRestoresBackup(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "nested", "file.bin"), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "nested", ".viper-v4-backup-test"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := openInstallationRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	journal := &applyJournal{
		root:       root,
		name:       applyJournalPrefix + "test",
		durability: DurabilityBuffered,
		data: applyJournalData{
			Version: applyJournalVersion,
			State:   "prepared",
			Entries: []applyJournalEntry{{
				Path:   "nested/file.bin",
				Temp:   "nested/.viper-v4-output-test",
				Backup: "nested/.viper-v4-backup-test",
				State:  "backed-up",
			}},
		},
	}
	file, err := root.root.OpenFile(journal.name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := journal.persist(); err != nil {
		t.Fatal(err)
	}
	if err := recoverApplyTransactions(root, DurabilityBuffered); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(base, "nested", "file.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != "original" {
		t.Fatalf("recovered data = %q", actual)
	}
}
