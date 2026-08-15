package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Job struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	Schedule   string `json:"schedule"`
	Curl       string `json:"curl"`
	Retries    int    `json:"retries"`
	RetryDelay int    `json:"retry_delay"`
	Enabled    bool   `json:"enabled"`
}

type Store interface {
	Load() ([]Job, error)
	Save([]Job) error
}

type FileStore struct {
	Path string
}

func NewFileStore(path string) *FileStore {
	return &FileStore{Path: path}
}

func (f *FileStore) Load() ([]Job, error) {
	data, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var jobs []Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", f.Path, err)
	}
	return jobs, nil
}

func (f *FileStore) Save(jobs []Job) error {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(f.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "jobs-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, f.Path)
}
