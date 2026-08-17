package runstore

import (
	"encoding/json"
	"fmt"
	"os"

	"cronix/internal/fsutil"
	"cronix/internal/model"
)

type RunStore interface {
	LoadRuns() ([]model.Run, error)
	SaveRuns([]model.Run) error
}

type FileRunStore struct {
	Path string
}

func NewFileRunStore(path string) *FileRunStore {
	return &FileRunStore{Path: path}
}

func (f *FileRunStore) LoadRuns() ([]model.Run, error) {
	data, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []model.Run
	if err := json.Unmarshal(data, &runs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.Path, err)
	}
	return runs, nil
}

func (f *FileRunStore) SaveRuns(runs []model.Run) error {
	data, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return fsutil.AtomicWriteFile("runs-*.tmp", f.Path, data)
}
