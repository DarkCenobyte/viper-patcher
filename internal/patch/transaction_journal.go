package patch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	applyJournalPrefix        = ".viper-transaction-"
	legacyApplyJournalVersion = 1
	applyJournalVersion       = 2
	maxApplyJournalSize       = 4 << 20
)

type applyJournalEntry struct {
	Path   string `json:"path"`
	Temp   string `json:"temp"`
	Backup string `json:"backup"`
	State  string `json:"state"`
}

type applyJournalData struct {
	Version int
	State   string
	Entries []applyJournalEntry
}

type applyJournalRecord struct {
	Version int                 `json:"version"`
	Kind    string              `json:"kind,omitempty"`
	State   string              `json:"state,omitempty"`
	Entry   uint32              `json:"entry,omitempty"`
	Entries []applyJournalEntry `json:"entries,omitempty"`
}

type applyJournal struct {
	root       *installationRoot
	name       string
	durability DurabilityMode
	data       applyJournalData
}

func beginApplyJournal(root *installationRoot, files []preparedFile, durability DurabilityMode) (*applyJournal, error) {
	file, name, err := createRootTemp(root.root, ".", applyJournalPrefix)
	if err != nil {
		return nil, fmt.Errorf("create apply transaction journal: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = root.root.Remove(name)
		return nil, err
	}
	journal := &applyJournal{
		root:       root,
		name:       name,
		durability: durability,
		data: applyJournalData{
			Version: applyJournalVersion,
			State:   "prepared",
			Entries: make([]applyJournalEntry, len(files)),
		},
	}
	for index := range files {
		journal.data.Entries[index] = applyJournalEntry{
			Path:   filepath.ToSlash(files[index].path),
			Temp:   filepath.ToSlash(files[index].temp),
			Backup: filepath.ToSlash(files[index].backup),
			State:  "prepared",
		}
	}
	if err := journal.persist(); err != nil {
		_ = root.root.Remove(name)
		return nil, err
	}
	return journal, nil
}

func (journal *applyJournal) mark(index int, state string) error {
	if journal == nil || index < 0 || index >= len(journal.data.Entries) {
		return fmt.Errorf("invalid apply journal transition")
	}
	current := journal.data.Entries[index].State
	if !validApplyJournalTransition(current, state) {
		return fmt.Errorf("invalid apply journal transition %q to %q", current, state)
	}
	journal.data.Entries[index].State = state
	return journal.persistRecord(applyJournalRecord{
		Version: applyJournalVersion,
		Kind:    "entry",
		State:   state,
		Entry:   uint32(index + 1),
	})
}

func (journal *applyJournal) markCommitted() error {
	if journal == nil {
		return fmt.Errorf("apply journal is unavailable")
	}
	for index := range journal.data.Entries {
		if journal.data.Entries[index].State != "replaced" {
			return fmt.Errorf("apply journal cannot commit before every entry is replaced")
		}
	}
	journal.data.State = "committed"
	return journal.persistRecord(applyJournalRecord{
		Version: applyJournalVersion,
		Kind:    "commit",
	})
}

// persist writes a complete snapshot. Production writes one snapshot when the
// journal is created and compact transition records afterwards. Keeping this
// helper also makes recovery fixtures explicit and self-contained.
func (journal *applyJournal) persist() error {
	if journal == nil {
		return fmt.Errorf("apply journal is unavailable")
	}
	return journal.persistRecord(applyJournalRecord{
		Version: applyJournalVersion,
		Kind:    "snapshot",
		State:   journal.data.State,
		Entries: journal.data.Entries,
	})
}

func (journal *applyJournal) persistRecord(record applyJournalRecord) error {
	file, err := journal.root.root.OpenFile(journal.name, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open apply transaction journal: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	writeErr := encoder.Encode(record)
	var syncErr error
	if writeErr == nil && journal.durability == DurabilityDurable {
		syncErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("persist apply transaction journal: %w", err)
	}
	if journal.durability == DurabilityDurable {
		return syncDirectoryV4(journal.root, ".")
	}
	return nil
}

func validApplyJournalTransition(current, next string) bool {
	return current == "prepared" && next == "backed-up" ||
		current == "backed-up" && next == "replaced"
}

func (journal *applyJournal) remove() error {
	if journal == nil {
		return nil
	}
	err := journal.root.root.Remove(journal.name)
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	if err == nil && journal.durability == DurabilityDurable {
		err = syncDirectoryV4(journal.root, ".")
	}
	return err
}

func recoverApplyTransactions(root *installationRoot, durability DurabilityMode) error {
	directory, err := root.root.Open(".")
	if err != nil {
		return fmt.Errorf("open installation root for transaction recovery: %w", err)
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("scan apply transaction journals: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), applyJournalPrefix) {
			continue
		}
		if err := recoverApplyJournal(root, entry.Name(), durability); err != nil {
			return err
		}
	}
	return nil
}

func recoverApplyJournal(root *installationRoot, name string, durability DurabilityMode) error {
	file, err := root.root.Open(name)
	if err != nil {
		return fmt.Errorf("open apply transaction journal %q: %w", name, err)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxApplyJournalSize+1))
	decoder.DisallowUnknownFields()
	var data applyJournalData
	decoded := false
	for {
		var record applyJournalRecord
		decodeErr := decoder.Decode(&record)
		if decodeErr == nil {
			if err := replayApplyJournalRecord(&data, &decoded, record); err != nil {
				_ = file.Close()
				return fmt.Errorf("decode apply transaction journal %q: %w", name, err)
			}
			continue
		}
		if errors.Is(decodeErr, io.EOF) {
			break
		}
		if decoded && errors.Is(decodeErr, io.ErrUnexpectedEOF) {
			// An append may have been interrupted. The last complete record is
			// still authoritative and makes recovery idempotent.
			break
		}
		_ = file.Close()
		return fmt.Errorf("decode apply transaction journal %q: %w", name, decodeErr)
	}
	closeErr := file.Close()
	if closeErr != nil {
		return fmt.Errorf("close apply transaction journal %q: %w", name, closeErr)
	}
	if !decoded {
		return fmt.Errorf("apply transaction journal %q contains no complete state", name)
	}
	if len(data.Entries) == 0 {
		return fmt.Errorf("unsupported apply transaction journal %q", name)
	}
	for index := range data.Entries {
		if err := validateJournalEntry(data.Entries[index]); err != nil {
			return fmt.Errorf("unsafe apply transaction journal %q entry %d: %w", name, index, err)
		}
	}

	var recoveryErrors []error
	if data.State == "committed" {
		for _, entry := range data.Entries {
			recoveryErrors = append(recoveryErrors,
				removeRootFileIfPresent(root, filepath.FromSlash(entry.Backup)),
				removeRootFileIfPresent(root, filepath.FromSlash(entry.Temp)),
			)
		}
	} else {
		for index := len(data.Entries) - 1; index >= 0; index-- {
			entry := data.Entries[index]
			path := filepath.FromSlash(entry.Path)
			backup := filepath.FromSlash(entry.Backup)
			temp := filepath.FromSlash(entry.Temp)
			if rootPathExists(root, backup) {
				if err := removeRootFileIfPresent(root, path); err != nil {
					recoveryErrors = append(recoveryErrors, err)
				} else if err := root.root.Rename(backup, path); err != nil {
					recoveryErrors = append(recoveryErrors, fmt.Errorf("restore %q from %q: %w", path, backup, err))
				}
			}
			recoveryErrors = append(recoveryErrors, removeRootFileIfPresent(root, temp))
		}
	}
	if err := errors.Join(recoveryErrors...); err != nil {
		return fmt.Errorf("recover apply transaction %q: %w", name, err)
	}
	if err := root.root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove recovered apply journal %q: %w", name, err)
	}
	if durability == DurabilityDurable {
		return syncDirectoryV4(root, ".")
	}
	return nil
}

func replayApplyJournalRecord(data *applyJournalData, decoded *bool, record applyJournalRecord) error {
	if data == nil || decoded == nil {
		return fmt.Errorf("apply journal replay state is unavailable")
	}
	switch record.Version {
	case legacyApplyJournalVersion:
		if record.Kind != "" || record.Entry != 0 || len(record.Entries) == 0 {
			return fmt.Errorf("invalid legacy apply journal snapshot")
		}
		*data = applyJournalData{
			Version: legacyApplyJournalVersion,
			State:   record.State,
			Entries: append([]applyJournalEntry(nil), record.Entries...),
		}
		*decoded = true
		return nil
	case applyJournalVersion:
	default:
		return fmt.Errorf("unsupported apply journal version %d", record.Version)
	}

	switch record.Kind {
	case "snapshot":
		if record.Entry != 0 || len(record.Entries) == 0 {
			return fmt.Errorf("invalid apply journal snapshot")
		}
		*data = applyJournalData{
			Version: applyJournalVersion,
			State:   record.State,
			Entries: append([]applyJournalEntry(nil), record.Entries...),
		}
		*decoded = true
		return nil
	case "entry":
		if !*decoded || data.Version != applyJournalVersion || record.Entry == 0 ||
			len(record.Entries) != 0 || record.State == "" {
			return fmt.Errorf("invalid apply journal entry transition")
		}
		index := int(record.Entry - 1)
		if index < 0 || index >= len(data.Entries) ||
			!validApplyJournalTransition(data.Entries[index].State, record.State) {
			return fmt.Errorf("invalid apply journal entry transition")
		}
		data.Entries[index].State = record.State
		return nil
	case "commit":
		if !*decoded || data.Version != applyJournalVersion || record.Entry != 0 ||
			record.State != "" || len(record.Entries) != 0 {
			return fmt.Errorf("invalid apply journal commit record")
		}
		for index := range data.Entries {
			if data.Entries[index].State != "replaced" {
				return fmt.Errorf("apply journal committed before every entry was replaced")
			}
		}
		data.State = "committed"
		return nil
	default:
		return fmt.Errorf("unsupported apply journal record %q", record.Kind)
	}
}

func validateJournalEntry(entry applyJournalEntry) error {
	path, err := localPatchPath(entry.Path)
	if err != nil {
		return err
	}
	temp, err := localPatchPath(entry.Temp)
	if err != nil {
		return err
	}
	backup, err := localPatchPath(entry.Backup)
	if err != nil {
		return err
	}
	if filepath.Clean(filepath.Dir(path)) != filepath.Clean(filepath.Dir(temp)) ||
		filepath.Clean(filepath.Dir(path)) != filepath.Clean(filepath.Dir(backup)) {
		return fmt.Errorf("transaction artifacts must share the target directory")
	}
	if !strings.HasPrefix(filepath.Base(temp), ".viper-v4-output-") ||
		!strings.HasPrefix(filepath.Base(backup), ".viper-v4-backup-") {
		return fmt.Errorf("unexpected transaction artifact name")
	}
	return nil
}

func rootPathExists(root *installationRoot, name string) bool {
	_, err := root.root.Lstat(name)
	return err == nil
}

func removeRootFileIfPresent(root *installationRoot, name string) error {
	err := root.root.Remove(name)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove transaction artifact %q: %w", name, err)
}
