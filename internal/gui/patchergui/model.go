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
}

type patcherState struct {
	mutex     sync.RWMutex
	selection patcherSelection
	active    bool
}

func (state *patcherState) SetPatch(path, digest string, parsed patchformat.Patch) bool {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.active {
		return false
	}
	state.selection.patchPath = path
	state.selection.patchHash = digest
	state.selection.parsed = parsed
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
