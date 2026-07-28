package workerbudget

import "testing"

func TestAutomaticAndEffective(t *testing.T) {
	automatic := Automatic()
	if automatic < 1 || automatic > Maximum() {
		t.Fatalf("automatic worker target = %d, maximum = %d", automatic, Maximum())
	}
	if effective := Effective(0); effective != automatic {
		t.Fatalf("effective automatic target = %d, want %d", effective, automatic)
	}
	if effective := Effective(1); effective != 1 {
		t.Fatalf("effective explicit target = %d", effective)
	}
	if effective := Effective(Maximum() + 1); effective != Maximum() {
		t.Fatalf("capped target = %d, want %d", effective, Maximum())
	}
}

func TestIsValid(t *testing.T) {
	if IsValid(-1) {
		t.Fatal("negative worker target must be rejected")
	}
	if !IsValid(0) || !IsValid(1) || !IsValid(Maximum()) {
		t.Fatal("automatic and supported explicit worker targets must be accepted")
	}
	if IsValid(Maximum() + 1) {
		t.Fatal("worker target above the logical CPU count must be rejected")
	}
}
