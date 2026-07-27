package patchergui

import (
	"fmt"
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

type patcherSelection struct {
	patchPath       string
	patchHash       string
	targetDirectory string
	parsed          patchformat.Patch
	prepared        *patch.PreparedPatch
}

type patcherState struct {
	mutex     sync.RWMutex
	selection patcherSelection
	active    bool
}

func (state *patcherState) SetPatch(path, digest string, parsed patchformat.Patch) bool {
	return state.setPatch(path, digest, parsed, nil)
}

func (state *patcherState) SetPreparedPatch(path, digest string, parsed patchformat.Patch, prepared *patch.PreparedPatch) bool {
	return state.setPatch(path, digest, parsed, prepared)
}

func (state *patcherState) setPatch(path, digest string, parsed patchformat.Patch, prepared *patch.PreparedPatch) bool {
	state.mutex.Lock()
	if state.active {
		state.mutex.Unlock()
		return false
	}
	previous := state.selection.prepared
	state.selection.patchPath = path
	state.selection.patchHash = digest
	state.selection.parsed = parsed
	state.selection.prepared = prepared
	state.mutex.Unlock()
	if previous != nil && previous != prepared {
		_ = previous.Close()
	}
	return true
}

func (state *patcherState) SetTargetDirectory(path string) bool {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.active {
		return false
	}
	state.selection.targetDirectory = path
	return true
}

func (state *patcherState) Snapshot() patcherSelection {
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	return state.selection
}

func (state *patcherState) Begin(direction patch.Direction) (patcherSelection, error) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.active {
		return patcherSelection{}, fmt.Errorf("a patch operation is already running")
	}
	if state.selection.patchPath == "" || state.selection.targetDirectory == "" {
		return patcherSelection{}, fmt.Errorf("select a patch and a target directory first")
	}
	if direction == patch.Reverse && !state.selection.parsed.Header.Reverse {
		return patcherSelection{}, fmt.Errorf("patch does not contain reverse differentials")
	}
	state.active = true
	return state.selection, nil
}

func (state *patcherState) End() {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.active = false
}

func (state *patcherState) Active() bool {
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	return state.active
}

func (state *patcherState) DetachPreparedPatch() *patch.PreparedPatch {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	prepared := state.selection.prepared
	state.selection.prepared = nil
	return prepared
}
