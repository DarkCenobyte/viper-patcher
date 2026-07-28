package patch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTransactionAddUsesCollisionIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installed.bin")
	if err := os.WriteFile(path, []byte("installed"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	expectation := fileExpectation{Identity: identity}
	transaction := newTransactionWithOperations(transactionOperations{})
	if err := transaction.Add("Data/File.bin", "temp-a", expectation); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Add("data/file.BIN", "temp-b", expectation); err == nil {
		t.Fatal("case-equivalent target was accepted")
	}
	if err := transaction.Add("other.bin", "temp-a", expectation); err == nil {
		t.Fatal("duplicate temporary path was accepted")
	}
	if len(transaction.files) != 1 || len(transaction.targetKeys) != 1 || len(transaction.temporaryPaths) != 1 {
		t.Fatalf("unexpected transaction indexes: files=%d targets=%d temporaries=%d", len(transaction.files), len(transaction.targetKeys), len(transaction.temporaryPaths))
	}
}
