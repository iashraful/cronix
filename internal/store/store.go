package store

import (
	"encoding/json"
	"fmt"
	"os"

	"cronix/internal/fsutil"
	"cronix/internal/model"
)

type Store interface {
	Load() ([]model.Job, error)
	Save([]model.Job) error
}

type FileStore struct {
	Path string
}

func NewFileStore(path string) *FileStore {
	return &FileStore{Path: path}
}

func (f *FileStore) Load() ([]model.Job, error) {
	data, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var jobs []model.Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.Path, err)
	}
	return jobs, nil
}

func (f *FileStore) Save(jobs []model.Job) error {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return fsutil.AtomicWriteFile("jobs-*.tmp", f.Path, data)
}
