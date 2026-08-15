package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	jobs     []Job
	loadErr  error
	numSaves int
}

func (m *memStore) Load() ([]Job, error) { return m.jobs, m.loadErr }
func (m *memStore) Save(j []Job) error   { m.jobs = j; m.numSaves++; return nil }

type failingStore struct{ memStore }

func (f *failingStore) Save([]Job) error { return errors.New("disk full") }

func newTestController(t *testing.T, store Store) *Controller {
	t.Helper()
	c, err := NewController(store, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

func TestNewControllerLoadsStoredJobs(t *testing.T) {
	c := newTestController(t, &memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
		{Id: "b", Schedule: "* * * * *", Curl: "curl http://y", Enabled: false},
	}})
	jobs := c.List()
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	if _, ok := c.entries["a"]; !ok {
		t.Error("enabled job must be registered with cron")
	}
	if _, ok := c.entries["b"]; ok {
		t.Error("disabled job must not be registered with cron")
	}
}

func TestNewControllerWithBadStoredJobFails(t *testing.T) {
	_, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "not-cron", Curl: "curl http://x", Enabled: true},
	}}, "/bin/true")
	if err == nil {
		t.Fatal("want error for invalid stored job")
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("error should name job id %q: %v", "a", err)
	}
}

func TestCreateGeneratesIdWhenEmpty(t *testing.T) {
	c := newTestController(t, &memStore{})
	j, err := c.Create(Job{Schedule: "* * * * *", Curl: "curl http://x", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if j.Id == "" {
		t.Fatal("id should be generated")
	}
	if !j.Enabled {
		t.Error("enabled should be preserved as true")
	}
	if _, err := c.Get(j.Id); err != nil {
		t.Errorf("created job should be gettable: %v", err)
	}
}

func TestCreateRejectsDuplicateId(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://y"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestCreateValidatesSchedule(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Create(Job{Id: "a", Schedule: "nope", Curl: "curl http://x"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestCreateValidatesCurlCommand(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "wget http://x"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation for non-curl command, got %v", err)
	}
}

func TestUpdateReplacesFieldsAndReconciles(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := c.Update("a", Job{Schedule: "0 0 * * *", Curl: "curl http://y", Enabled: false})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Schedule != "0 0 * * *" || got.Name != "" {
		t.Errorf("update wrong: %+v", got)
	}
	if _, ok := c.entries["a"]; ok {
		t.Error("job disabled by update should be unregistered")
	}
	if got.Enabled {
		t.Error("enabled should be false after update")
	}
}

func TestUpdateKeepsIdFromPath(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := c.Update("a", Job{Id: "other", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Id != "a" {
		t.Errorf("id should stay %q, got %q", "a", got.Id)
	}
}

func TestUpdateMissingJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Update("nope", Job{Schedule: "* * * * *", Curl: "curl http://x", Enabled: true})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteRemovesJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(c.List()) != 0 {
		t.Errorf("job should be gone, got %d", len(c.List()))
	}
	if _, err := c.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteMissingJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	if err := c.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestEnableAndDisableToggle(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: false}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := c.entries["a"]; ok {
		t.Error("disabled job should not be registered")
	}
	if err := c.Enable("a"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, ok := c.entries["a"]; !ok {
		t.Error("enabled job should be registered")
	}
	if err := c.Disable("a"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, ok := c.entries["a"]; ok {
		t.Error("disabled job should be unregistered")
	}
}

func TestCreateRollsBackOnSaveFailure(t *testing.T) {
	c := newTestController(t, &failingStore{})
	_, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"})
	if !errors.Is(err, ErrStorage) {
		t.Fatalf("want ErrStorage, got %v", err)
	}
	if len(c.List()) != 0 {
		t.Errorf("in-memory list should be rolled back, got %d", len(c.List()))
	}
	if _, ok := c.entries["a"]; ok {
		t.Error("cron entry should be rolled back")
	}
}

func TestUpdateRollsBackOnSaveFailure(t *testing.T) {
	c := newTestController(t, &failingStore{memStore{jobs: []Job{
		{Id: "a", Schedule: "*/5 * * * *", Curl: "curl http://x", Enabled: true},
	}}})
	if _, ok := c.entries["a"]; !ok {
		t.Fatal("seeded enabled job should be registered with cron")
	}
	_, err := c.Update("a", Job{Schedule: "0 0 * * *", Curl: "curl http://y", Enabled: false})
	if !errors.Is(err, ErrStorage) {
		t.Fatalf("want ErrStorage, got %v", err)
	}
	got, err := c.Get("a")
	if err != nil {
		t.Fatalf("job should remain gettable after rollback: %v", err)
	}
	if got.Schedule != "*/5 * * * *" || got.Curl != "curl http://x" || !got.Enabled {
		t.Errorf("in-memory job should match original, got %+v", got)
	}
	if _, ok := c.entries["a"]; !ok {
		t.Error("cron entry should match original enabled job after rollback")
	}
}

func TestListPreservesOrder(t *testing.T) {
	c := newTestController(t, &memStore{})
	for _, id := range []string{"c", "a", "b"} {
		if _, err := c.Create(Job{Id: id, Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	jobs := c.List()
	for i, want := range []string{"c", "a", "b"} {
		if jobs[i].Id != want {
			t.Errorf("order[%d]: want %s, got %s", i, want, jobs[i].Id)
		}
	}
}

func TestControllerConcurrentMutationsNoPanic(t *testing.T) {
	mem := &memStore{}
	c := newTestController(t, mem)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("j%d", i%4)
			switch i % 3 {
			case 0:
				_, _ = c.Create(Job{Id: id, Schedule: "* * * * *", Curl: "curl http://x"})
			case 1:
				_, _ = c.Update(id, Job{Schedule: "*/5 * * * *", Curl: "curl http://y", Enabled: true})
			case 2:
				_ = c.Delete(id)
			}
		}(i)
	}
	wg.Wait()
	c.List()
	if n := len(mem.jobs); n > 4 {
		t.Errorf("more jobs than distinct ids possible: %d", n)
	}
}

// TestScheduledFireUsesLiveCurlAfterUpdate verifies that a fire reads the LIVE
// job from the controller at run time: updating ONLY curl (leaving schedule and
// enabled unchanged, so the cron entry is untouched) must change what a
// scheduled fire executes.
func TestScheduledFireUsesLiveCurlAfterUpdate(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	// /usr/bin/env acts as the "curl" binary: it execs its first argument, so
	// "curl true" exits 0 and "curl false" exits non-zero.
	c, err := NewController(&memStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	if _, err := c.Create(Job{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := c.Update("a", Job{Id: "a", Schedule: "* * * * *", Curl: "curl false", Enabled: true}); err != nil {
		t.Fatalf("update: %v", err)
	}
	entryID, ok := c.entries["a"]
	if !ok {
		t.Fatal("enabled job must stay registered after a curl-only update")
	}
	logs.Reset()
	c.cron.Entry(entryID).Job.Run()
	got := logs.String()
	if !strings.Contains(got, "attempt=1/1") {
		t.Fatalf("scheduled fire did not run; logs:\n%s", got)
	}
	if !strings.Contains(got, "exit=1") {
		t.Errorf("fire should execute the LIVE curl %q (exit=1 via /usr/bin/false), but logs show a stale run:\n%s",
			"curl false", got)
	}
}

// TestConcurrentRunsDoNotInterleave proves that two manual runs of the same job
// are serialized (per the design spec) rather than executing in parallel.
func TestConcurrentRunsDoNotInterleave(t *testing.T) {
	// /bin/sleep sleeps for its first argument, so each run takes ~600ms.
	c, err := NewController(&memStore{jobs: []Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl 0.6", Enabled: true},
	}}, "/bin/sleep")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Run("a"); err != nil {
				t.Errorf("run: %v", err)
			}
		}()
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed < 1100*time.Millisecond {
		t.Errorf("two runs of a 600ms job must not interleave; want serialized ~1.2s, got %v", elapsed)
	}
}
