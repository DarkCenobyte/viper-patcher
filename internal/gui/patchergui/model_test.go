package patchergui

import (
	"sync"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

func TestPatcherStateCapturesAndLocksSelection(t *testing.T) {
	state := &patcherState{}
	parsed := patchformat.Patch{Header: patchformat.Header{Reverse: true}}
	if !state.SetPatch("first.vipr", "first-hash", parsed) || !state.SetTargetDirectory("first-target") {
		t.Fatal("initial selection failed")
	}
	selection, err := state.Begin(patch.Forward)
	if err != nil {
		t.Fatal(err)
	}
	if selection.patchPath != "first.vipr" || selection.patchHash != "first-hash" || selection.targetDirectory != "first-target" {
		t.Fatalf("unexpected captured selection: %#v", selection)
	}
	if state.SetPatch("second.vipr", "second-hash", parsed) || state.SetTargetDirectory("second-target") {
		t.Fatal("selection changed during an active operation")
	}
	state.End()
	if !state.SetPatch("second.vipr", "second-hash", parsed) || !state.SetTargetDirectory("second-target") {
		t.Fatal("selection remained locked after operation")
	}
}

func TestPatcherStateConcurrentAccess(t *testing.T) {
	state := &patcherState{}
	parsed := patchformat.Patch{Header: patchformat.Header{Reverse: true}}
	state.SetPatch("update.vipr", "update-hash", parsed)
	state.SetTargetDirectory("target")

	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				_ = state.Snapshot()
				_ = state.Active()
			}
		}()
	}
	wait.Wait()
}

func TestPatcherStateRejectsInvalidBegin(t *testing.T) {
	state := &patcherState{}
	if _, err := state.Begin(patch.Forward); err == nil {
		t.Fatal("expected missing selection to be rejected")
	}
	state.SetPatch("update.vipr", "update-hash", patchformat.Patch{})
	state.SetTargetDirectory("target")
	if _, err := state.Begin(patch.Reverse); err == nil {
		t.Fatal("expected unavailable reverse direction to be rejected")
	}
}
