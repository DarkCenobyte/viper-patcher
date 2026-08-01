package nativev4

import (
	"context"
	"testing"
)

func TestSessionPoolReusesInitialSession(t *testing.T) {
	initial, err := NewSession(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	pool, err := NewSessionPoolWithInitial(initial, 2, nil, nil, nil, IOAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if got := len(pool.all); got != 2 {
		t.Fatalf("session count = %d, want 2", got)
	}
	acquired, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acquired != initial {
		t.Fatal("pool did not expose the transferred initial session first")
	}
	pool.Release(acquired)
}

func TestSessionPoolWithInitialRejectsInvalidCount(t *testing.T) {
	initial, err := NewSession(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewSessionPoolWithInitial(initial, 0, nil, nil, nil, IOAuto); err == nil {
		initial.Close()
		t.Fatal("zero-sized pool unexpectedly succeeded")
	}
	// Ownership transfers on every call, including failed construction.
	if initial.native != nil {
		initial.Close()
		t.Fatal("failed pool construction did not close the initial session")
	}
}
