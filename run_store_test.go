package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunJSONKeys(t *testing.T) {
	r := Run{
		JobId: "a", Trigger: "manual", Time: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
		Status: "ok", ExitCode: 0,
		Results: []Result{{Attempt: 1, Total: 1, ExitCode: 0, Output: "hi"}},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"job_id", "trigger", "time", "status", "exit_code", "results"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing JSON key %q in: %s", k, b)
		}
	}
}

func TestFileRunStoreLoadMissingReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.json")
	got, err := NewFileRunStore(path).LoadRuns()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no runs, got %d", len(got))
	}
}

func TestFileRunStoreSaveThenLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.json")
	fs := NewFileRunStore(path)
	want := []Run{
		{JobId: "a", Trigger: "manual", Time: time.Now().UTC(), Status: "ok", ExitCode: 0},
		{JobId: "b", Trigger: "scheduled", Time: time.Now().UTC().Add(-time.Minute), Status: "failed", ExitCode: 6,
			Results: []Result{{Attempt: 1, Total: 1, ExitCode: 6, Output: "x"}}},
	}
	if err := fs.SaveRuns(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := fs.LoadRuns()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 runs, got %d", len(got))
	}
	if got[0].JobId != "a" || got[1].Status != "failed" || len(got[1].Results) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestFileRunStoreSaveCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "runs.json")
	if err := NewFileRunStore(path).SaveRuns([]Run{{JobId: "a", Status: "ok"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist after save: %v", err)
	}
}

func TestFileRunStoreLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewFileRunStore(path).LoadRuns()
	if err == nil {
		t.Fatal("want error for corrupt store file")
	}
	if !strings.Contains(err.Error(), "runs.json") {
		t.Errorf("error should name the store path: %v", err)
	}
}

func TestFileRunStoreSaveLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")
	if err := NewFileRunStore(path).SaveRuns([]Run{{JobId: "a", Status: "ok"}}); err != nil {
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
