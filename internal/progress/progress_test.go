package progress

import "testing"

func TestReport(t *testing.T) {
	Report(nil, Event{})
	called := false
	want := Event{FileIndex: 1, FileCount: 2, Path: "file.bin", Stage: Stage("testing")}
	Report(func(got Event) {
		called = true
		if got != want {
			t.Fatalf("event = %#v, want %#v", got, want)
		}
	}, want)
	if !called {
		t.Fatal("callback was not called")
	}
}
