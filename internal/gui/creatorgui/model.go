package creatorgui

import (
	"fmt"
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
)

type filePairModel struct {
	mutex sync.RWMutex
	pairs []patch.FilePair
}

func (model *filePairModel) Add(sourcePath, targetPath string) error {
	if sourcePath == "" || targetPath == "" {
		return fmt.Errorf("both source and target files are required")
	}
	model.mutex.Lock()
	defer model.mutex.Unlock()
	model.pairs = append(model.pairs, patch.FilePair{SourcePath: sourcePath, TargetPath: targetPath})
	return nil
}

func (model *filePairModel) Remove(index int) bool {
	model.mutex.Lock()
	defer model.mutex.Unlock()
	if index < 0 || index >= len(model.pairs) {
		return false
	}
	model.pairs = append(model.pairs[:index], model.pairs[index+1:]...)
	return true
}

func (model *filePairModel) Clear() {
	model.mutex.Lock()
	defer model.mutex.Unlock()
	model.pairs = nil
}

func (model *filePairModel) Pairs() []patch.FilePair {
	model.mutex.RLock()
	defer model.mutex.RUnlock()
	return append([]patch.FilePair(nil), model.pairs...)
}

func (model *filePairModel) Len() int {
	model.mutex.RLock()
	defer model.mutex.RUnlock()
	return len(model.pairs)
}
