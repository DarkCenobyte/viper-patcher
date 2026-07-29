//go:build ignore

package patch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DarkCenobyte/viper-patcher/internal/pathutil"
)

type transactionOperations struct {
	createTemp    func(string, string) (*os.File, string, error)
	rename        func(string, string) error
	remove        func(string) error
	verify        func(string, fileExpectation) error
	syncDirectory func(string) error
}

type transactionFile struct {
	target         string
	temporary      string
	backup         string
	expectation    fileExpectation
	backedUp       bool
	committed      bool
	directoryDirty bool
}

// Transaction replaces a group of prepared files and performs a best-effort
// rollback when a later handled replacement fails. It does not provide
// crash-consistent multi-file transactions across power loss or kernel failure.
type Transaction struct {
	files          []transactionFile
	targetKeys     map[string]struct{}
	temporaryPaths map[string]struct{}
	operations     transactionOperations
	finished       bool
}

func newRootTransaction(root *os.Root) *Transaction {
	return newTransactionWithOperations(transactionOperations{
		createTemp: func(directory, pattern string) (*os.File, string, error) {
			return createRootTemp(root, directory, pattern)
		},
		rename: root.Rename,
		remove: root.Remove,
		verify: func(path string, expectation fileExpectation) error {
			return verifyRootFileExpectation(root, path, expectation)
		},
		syncDirectory: func(path string) error {
			return syncRootDirectory(root, path)
		},
	})
}

func newTransactionWithOperations(operations transactionOperations) *Transaction {
	return &Transaction{
		targetKeys:     make(map[string]struct{}),
		temporaryPaths: make(map[string]struct{}),
		operations:     operations,
	}
}

// Add registers one prepared replacement and the identity of the validated file.
func (transaction *Transaction) Add(target, temporary string, expectation fileExpectation) error {
	if transaction.finished {
		return fmt.Errorf("transaction is already finished")
	}
	if target == "" || temporary == "" || expectation.Identity == nil {
		return fmt.Errorf("transaction replacement is incomplete")
	}
	if transaction.targetKeys == nil {
		transaction.targetKeys = make(map[string]struct{})
	}
	if transaction.temporaryPaths == nil {
		transaction.temporaryPaths = make(map[string]struct{})
	}
	targetKey := pathutil.CaseInsensitiveKey(target)
	if _, exists := transaction.targetKeys[targetKey]; exists {
		return fmt.Errorf("transaction contains duplicate target or temporary path %q", target)
	}
	if _, exists := transaction.temporaryPaths[temporary]; exists {
		return fmt.Errorf("transaction contains duplicate target or temporary path %q", target)
	}
	transaction.files = append(transaction.files, transactionFile{
		target:      target,
		temporary:   temporary,
		expectation: expectation,
	})
	transaction.targetKeys[targetKey] = struct{}{}
	transaction.temporaryPaths[temporary] = struct{}{}
	return nil
}

// Commit replaces every registered file. Any replacement failure includes
// rollback errors. Cleanup errors after a successful commit are returned as a
// CommittedWarning and do not mean the replacements failed.
func (transaction *Transaction) Commit() error {
	if transaction.finished {
		return fmt.Errorf("transaction is already finished")
	}
	for index := range transaction.files {
		file := &transaction.files[index]
		if err := transaction.operations.verify(file.target, file.expectation); err != nil {
			return transaction.fail(index-1, err)
		}
		if err := transaction.reserveBackup(file); err != nil {
			return transaction.fail(index-1, err)
		}
		if err := transaction.operations.rename(file.target, file.backup); err != nil {
			return transaction.fail(index-1, fmt.Errorf("backup %q: %w", file.target, err))
		}
		file.backedUp = true
		file.directoryDirty = true
		if err := transaction.operations.rename(file.temporary, file.target); err != nil {
			return transaction.fail(index, fmt.Errorf("replace %q: %w", file.target, err))
		}
		file.committed = true
		file.temporary = ""
	}

	if syncError := transaction.syncDirectories(); syncError != nil {
		transaction.finished = true
		return committedWarning("file replacement", syncError)
	}
	cleanupError := transaction.removeBackups()
	transaction.finished = true
	return committedWarning("file replacement", cleanupError)
}

// Cleanup removes prepared files when a transaction is abandoned before commit.
func (transaction *Transaction) Cleanup() error {
	if transaction.finished {
		return nil
	}
	cleanupError := transaction.cleanupPreparedFiles()
	transaction.finished = true
	return cleanupError
}

func (transaction *Transaction) reserveBackup(file *transactionFile) error {
	backup, backupPath, err := transaction.operations.createTemp(filepath.Dir(file.target), ".viper-patcher-backup-")
	if err != nil {
		return fmt.Errorf("reserve backup path for %q: %w", file.target, err)
	}
	file.backup = backupPath
	if err := backup.Close(); err != nil {
		removeError := transaction.operations.remove(file.backup)
		return errors.Join(
			fmt.Errorf("close backup placeholder for %q: %w", file.target, err),
			wrapRemoveError("remove backup placeholder", file.backup, removeError),
		)
	}
	if err := transaction.operations.remove(file.backup); err != nil {
		return fmt.Errorf("release backup path for %q: %w", file.target, err)
	}
	return nil
}

func (transaction *Transaction) fail(lastIndex int, operationError error) error {
	rollbackError := transaction.rollback(lastIndex)
	cleanupError := transaction.cleanupPreparedFiles()
	syncError := transaction.syncDirectories()
	transaction.finished = true
	return errors.Join(
		operationError,
		wrapJoinedError("rollback failed", rollbackError),
		wrapJoinedError("cleanup after failed transaction", cleanupError),
		wrapJoinedError("sync directories after rollback", syncError),
	)
}

func (transaction *Transaction) cleanupPreparedFiles() error {
	var cleanupErrors []error
	for index := range transaction.files {
		file := &transaction.files[index]
		if file.temporary != "" {
			if err := transaction.operations.remove(file.temporary); err != nil && !os.IsNotExist(err) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove temporary file %q: %w", file.temporary, err))
			} else {
				file.temporary = ""
			}
		}
		if file.backup != "" && !file.backedUp {
			if err := transaction.operations.remove(file.backup); err != nil && !os.IsNotExist(err) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove backup placeholder %q: %w", file.backup, err))
			} else {
				file.backup = ""
			}
		}
	}
	return errors.Join(cleanupErrors...)
}

func (transaction *Transaction) rollback(lastIndex int) error {
	var rollbackErrors []error
	if lastIndex >= len(transaction.files) {
		lastIndex = len(transaction.files) - 1
	}
	for index := lastIndex; index >= 0; index-- {
		file := &transaction.files[index]
		if !file.backedUp {
			continue
		}
		if file.committed {
			if err := transaction.operations.remove(file.target); err != nil && !os.IsNotExist(err) {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("remove replacement %q: %w", file.target, err))
				continue
			}
		}
		if err := transaction.operations.rename(file.backup, file.target); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore %q: %w", file.target, err))
			continue
		}
		file.backedUp = false
		file.committed = false
	}
	return errors.Join(rollbackErrors...)
}

func (transaction *Transaction) removeBackups() error {
	var cleanupErrors []error
	for index := range transaction.files {
		file := &transaction.files[index]
		if file.backup == "" {
			continue
		}
		if err := transaction.operations.remove(file.backup); err != nil && !os.IsNotExist(err) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove committed backup %q: %w", file.backup, err))
			continue
		}
		file.backup = ""
		file.backedUp = false
	}
	return errors.Join(cleanupErrors...)
}

func (transaction *Transaction) syncDirectories() error {
	if transaction.operations.syncDirectory == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(transaction.files))
	var syncErrors []error
	for index := range transaction.files {
		file := &transaction.files[index]
		if !file.directoryDirty {
			continue
		}
		directory := filepath.Clean(filepath.Dir(file.target))
		if _, exists := seen[directory]; exists {
			continue
		}
		seen[directory] = struct{}{}
		if err := transaction.operations.syncDirectory(directory); err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("sync committed directory %q: %w", directory, err))
		}
	}
	return errors.Join(syncErrors...)
}

func wrapRemoveError(operation, path string, err error) error {
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("%s %q: %w", operation, path, err)
}

func wrapJoinedError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
