package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStoreLoadMissingReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	s, err := NewFileStore(path).Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s) != 0 {
		t.Fatalf("want no jobs, got %d", len(s))
	}
}

func TestFileStoreSaveThenLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	fs := NewFileStore(path)
	want := []Job{
		{Id: "a", Name: "alpha", Schedule: "*/5 * * * *", Curl: "curl -s https://a", Retries: 1, RetryDelay: 7, Enabled: true},
		{Id: "b", Schedule: "0 9 * * 1-5", Curl: "curl -s https://b", Enabled: false},
	}
	if err := fs.Save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := fs.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(got))
	}
	for i, j := range want {
		if got[i] != j {
			t.Errorf("job %d: want %+v, got %+v", i, j, got[i])
		}
	}
}

func TestFileStoreSaveCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "jobs.json")
	if err := NewFileStore(path).Save([]Job{{Id: "x", Enabled: true}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist after save: %v", err)
	}
}

func TestFileStoreLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewFileStore(path).Load()
	if err == nil {
		t.Fatal("want error for corrupt store file")
	}
	if !strings.Contains(err.Error(), "jobs.json") {
		t.Errorf("error should name the store path: %v", err)
	}
}

func TestFileStoreSaveLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.json")
	if err := NewFileStore(path).Save([]Job{{Id: "x", Enabled: true}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
