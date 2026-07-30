package creatorcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DarkCenobyte/viper-patcher/internal/patch"
)

const maxFilePairsJSONBytes = 16 << 20

type filePairJSON struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

func loadFilePairsJSON(path string) (filePairList, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("file-pairs JSON path must not be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve file-pairs JSON: %w", err)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, fmt.Errorf("open file-pairs JSON: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(io.LimitReader(file, maxFilePairsJSONBytes+1))
	decoder.DisallowUnknownFields()
	var values []filePairJSON
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("decode file-pairs JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("file-pairs JSON contains more than one value")
		}
		return nil, fmt.Errorf("decode trailing file-pairs JSON: %w", err)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("file-pairs JSON must contain at least one pair")
	}

	base := filepath.Dir(absolute)
	pairs := make(filePairList, 0, len(values))
	for index, value := range values {
		source := strings.TrimSpace(value.Source)
		target := strings.TrimSpace(value.Target)
		if source == "" || target == "" {
			return nil, fmt.Errorf("file-pairs JSON entry %d requires non-empty source and target", index)
		}
		if !filepath.IsAbs(source) {
			source = filepath.Join(base, source)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(base, target)
		}
		pairs = append(pairs, patch.FilePair{
			SourcePath: filepath.Clean(source),
			TargetPath: filepath.Clean(target),
		})
	}
	return pairs, nil
}
