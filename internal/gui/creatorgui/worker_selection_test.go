package creatorgui

import "testing"

func TestSelectedWorkerBudget(t *testing.T) {
	if workers, err := selectedWorkerBudget(automaticWorkerOption); err != nil || workers != 0 {
		t.Fatalf("automatic workers = %d, %v", workers, err)
	}
	if workers, err := selectedWorkerBudget("2"); err != nil || workers != 2 {
		t.Fatalf("explicit workers = %d, %v", workers, err)
	}
	for _, value := range []string{"", "invalid", "0"} {
		if _, err := selectedWorkerBudget(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}
