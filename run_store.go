package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type Run struct {
	JobId    string    `json:"job_id"`
	Trigger  string    `json:"trigger"`
	Time     time.Time `json:"time"`
	Status   string    `json:"status"`
	ExitCode int       `json:"exit_code"`
	Results  []Result  `json:"results"`
}

type RunSummary struct {
	Status   string    `json:"status"`
	ExitCode int       `json:"exit_code"`
	Time     time.Time `json:"time"`
}

type RunStore interface {
	LoadRuns() ([]Run, error)
	SaveRuns([]Run) error
}

type FileRunStore struct {
	Path string
}

func NewFileRunStore(path string) *FileRunStore {
	return &FileRunStore{Path: path}
}

func (f *FileRunStore) LoadRuns() ([]Run, error) {
	data, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []Run
	if err := json.Unmarshal(data, &runs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.Path, err)
	}
	return runs, nil
}

func (f *FileRunStore) SaveRuns(runs []Run) error {
	data, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile("runs-*.tmp", f.Path, data)
}
