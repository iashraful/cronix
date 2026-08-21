package controller

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

	"cronix/internal/model"
	"cronix/internal/runstore"
	"cronix/internal/store"
)

type memStore struct {
	jobs     []model.Job
	loadErr  error
	numSaves int
}

func (m *memStore) Load() ([]model.Job, error) { return m.jobs, m.loadErr }
func (m *memStore) Save(j []model.Job) error   { m.jobs = j; m.numSaves++; return nil }

type failingStore struct{ memStore }

func (f *failingStore) Save([]model.Job) error { return errors.New("disk full") }

type memRunStore struct {
	runs     []model.Run
	loadErr  error
	saveErr  error
	numSaves int
}

func (m *memRunStore) LoadRuns() ([]model.Run, error) { return m.runs, m.loadErr }
func (m *memRunStore) SaveRuns(r []model.Run) error {
	m.runs = r
	m.numSaves++
	if m.saveErr != nil {
		return m.saveErr
	}
	return nil
}

var _ runstore.RunStore = (*memRunStore)(nil)

func newTestController(t *testing.T, store store.Store) *Controller {
	t.Helper()
	c, err := NewController(store, &memRunStore{}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

func TestNewControllerLoadsStoredJobs(t *testing.T) {
	c := newTestController(t, &memStore{jobs: []model.Job{
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
	_, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "not-cron", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{}, "/bin/true")
	if err == nil {
		t.Fatal("want error for invalid stored job")
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("error should name job id %q: %v", "a", err)
	}
}

func TestCreateGeneratesIdWhenEmpty(t *testing.T) {
	c := newTestController(t, &memStore{})
	j, err := c.Create(model.Job{Schedule: "* * * * *", Curl: "curl http://x", Enabled: true})
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
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://y"})
	if !errors.Is(err, model.ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestCreateValidatesSchedule(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Create(model.Job{Id: "a", Schedule: "nope", Curl: "curl http://x"})
	if !errors.Is(err, model.ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestCreateValidatesCurlCommand(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "wget http://x"})
	if !errors.Is(err, model.ErrValidation) {
		t.Fatalf("want ErrValidation for non-curl command, got %v", err)
	}
}

func TestUpdateReplacesFieldsAndReconciles(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := c.Update("a", model.Job{Schedule: "0 0 * * *", Curl: "curl http://y", Enabled: false})
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
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := c.Update("a", model.Job{Id: "other", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Id != "a" {
		t.Errorf("id should stay %q, got %q", "a", got.Id)
	}
}

func TestUpdateMissingJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	_, err := c.Update("nope", model.Job{Schedule: "* * * * *", Curl: "curl http://x", Enabled: true})
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteRemovesJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(c.List()) != 0 {
		t.Errorf("job should be gone, got %d", len(c.List()))
	}
	if _, err := c.Get("a"); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("want ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteMissingJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	if err := c.Delete("nope"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestEnableAndDisableToggle(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: false}); err != nil {
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
	_, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl http://x"})
	if !errors.Is(err, model.ErrStorage) {
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
	c := newTestController(t, &failingStore{memStore{jobs: []model.Job{
		{Id: "a", Schedule: "*/5 * * * *", Curl: "curl http://x", Enabled: true},
	}}})
	if _, ok := c.entries["a"]; !ok {
		t.Fatal("seeded enabled job should be registered with cron")
	}
	_, err := c.Update("a", model.Job{Schedule: "0 0 * * *", Curl: "curl http://y", Enabled: false})
	if !errors.Is(err, model.ErrStorage) {
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
		if _, err := c.Create(model.Job{Id: id, Schedule: "* * * * *", Curl: "curl http://x"}); err != nil {
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
				_, _ = c.Create(model.Job{Id: id, Schedule: "* * * * *", Curl: "curl http://x"})
			case 1:
				_, _ = c.Update(id, model.Job{Schedule: "*/5 * * * *", Curl: "curl http://y", Enabled: true})
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
	c, err := NewController(&memStore{}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	if _, err := c.Create(model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := c.Update("a", model.Job{Id: "a", Schedule: "* * * * *", Curl: "curl false", Enabled: true}); err != nil {
		t.Fatalf("update: %v", err)
	}
	entryID, ok := c.entries["a"]
	if !ok {
		t.Fatal("enabled job must stay registered after a curl-only update")
	}
	logs.Reset()
	c.cron.Entry(entryID).Job.Run()
	got := logs.String()
	if !strings.Contains(got, "attempt 1/1 exit=1") {
		t.Fatalf("scheduled fire did not run the live curl; logs:\n%s", got)
	}
	if !strings.Contains(got, "result: FAILED") {
		t.Errorf("failed scheduled fire should log the run block with result: FAILED:\n%s", got)
	}
}

// TestConcurrentRunsDoNotInterleave proves that two manual runs of the same job
// are serialized (per the design spec) rather than executing in parallel.
func TestConcurrentRunsDoNotInterleave(t *testing.T) {
	// /bin/sleep sleeps for its first argument, so each run takes ~600ms.
	c, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl 0.6", Enabled: true},
	}}, &memRunStore{}, "/bin/sleep")
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

func TestRunRecordsManualHistory(t *testing.T) {
	c, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	if _, err := c.Run("a"); err != nil {
		t.Fatalf("run: %v", err)
	}
	runs, err := c.History("a")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	if runs[0].Trigger != "manual" || runs[0].Status != "ok" || runs[0].ExitCode != 0 {
		t.Errorf("run record wrong: %+v", runs[0])
	}
	ls := c.LastRun("a")
	if ls == nil || ls.Status != "ok" {
		t.Errorf("last run summary wrong: %+v", ls)
	}
}

func TestRunRecordsScheduledHistory(t *testing.T) {
	c, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	c.fire("a")
	runs, err := c.History("a")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(runs) != 1 || runs[0].Trigger != "scheduled" {
		t.Errorf("scheduled fire should record trigger=scheduled: %+v", runs)
	}
}

func TestRecordRunStatusDerivation(t *testing.T) {
	c := newTestController(t, &memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}})
	c.recordRun(model.Job{Id: "a"}, "manual", []model.Result{{Attempt: 1, Total: 1, ExitCode: 0}}, nil)
	c.recordRun(model.Job{Id: "a"}, "manual", []model.Result{{Attempt: 1, Total: 1, ExitCode: 6}},
		fmt.Errorf("%w: job a failed", model.ErrCommandFailed))
	c.recordRun(model.Job{Id: "a"}, "manual", nil, errors.New("cannot start /bin/true"))
	runs, _ := c.History("a")
	if len(runs) != 3 {
		t.Fatalf("want 3 runs, got %d", len(runs))
	}
	if runs[0].Status != "error" || runs[1].Status != "failed" || runs[2].Status != "ok" {
		t.Errorf("status order wrong (newest-first): %+v", runs)
	}
	if runs[1].ExitCode != 6 {
		t.Errorf("failed run should carry last exit: %+v", runs[1])
	}
}

func TestRecordRunTrimsToCap(t *testing.T) {
	c := newTestController(t, &memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}})
	for i := 0; i < runHistoryCap+10; i++ {
		c.recordRun(model.Job{Id: "a"}, "manual", []model.Result{{Attempt: 1, Total: 1, ExitCode: 0}}, nil)
	}
	runs, _ := c.History("a")
	if len(runs) != runHistoryCap {
		t.Fatalf("want %d runs, got %d", runHistoryCap, len(runs))
	}
}

func TestRecordRunSaveFailureDoesNotFail(t *testing.T) {
	c, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, &memRunStore{saveErr: errors.New("disk full")}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	c.recordRun(model.Job{Id: "a"}, "manual", []model.Result{{Attempt: 1, Total: 1, ExitCode: 0}}, nil)
	runs, err := c.History("a")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(runs) != 1 {
		t.Fatal("run must stay in the in-memory ledger even when persistence fails")
	}
}

func TestHistoryMissingJob(t *testing.T) {
	c := newTestController(t, &memStore{})
	if _, err := c.History("nope"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestLastRunNilWhenEmpty(t *testing.T) {
	c := newTestController(t, &memStore{})
	if ls := c.LastRun("nope"); ls != nil {
		t.Errorf("want nil LastRun, got %+v", ls)
	}
}

func TestNextRun(t *testing.T) {
	c := newTestController(t, &memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
		{Id: "b", Schedule: "* * * * *", Curl: "curl http://x", Enabled: false},
	}})
	next, err := c.NextRun("a")
	if err != nil {
		t.Fatalf("next run: %v", err)
	}
	if next == nil || !next.After(time.Now()) {
		t.Errorf("enabled job should have a future next run: %+v", next)
	}
	next, err = c.NextRun("b")
	if err != nil || next != nil {
		t.Errorf("disabled job should have nil next run, got %v (%v)", next, err)
	}
	if _, err := c.NextRun("nope"); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestDeletePrunesHistory(t *testing.T) {
	c, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	if _, err := c.Run("a"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := c.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ls := c.LastRun("a"); ls != nil {
		t.Errorf("deleted job must not retain last run: %+v", ls)
	}
}

func TestRecordRunSkipsDeletedJob(t *testing.T) {
	c, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl true", Enabled: true},
	}}, &memRunStore{}, "/usr/bin/env")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	if err := c.Delete("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	c.recordRun(model.Job{Id: "a"}, "scheduled",
		[]model.Result{{Attempt: 1, Total: 1, ExitCode: 0, Output: "late fire"}}, nil)
	if ls := c.LastRun("a"); ls != nil {
		t.Errorf("a fire racing a delete must not retain a run: %+v", ls)
	}
}

func TestNewControllerLoadsAndPrunesRunHistory(t *testing.T) {
	store := &memRunStore{runs: []model.Run{
		{JobId: "a", Trigger: "manual", Time: time.Now().UTC(), Status: "ok", ExitCode: 0},
		{JobId: "ghost", Trigger: "manual", Time: time.Now().UTC(), Status: "ok", ExitCode: 0},
	}}
	c, err := NewController(&memStore{jobs: []model.Job{
		{Id: "a", Schedule: "* * * * *", Curl: "curl http://x", Enabled: true},
	}}, store, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	runs, _ := c.History("a")
	if len(runs) != 1 {
		t.Fatalf("want loaded run for job a, got %d", len(runs))
	}
	if ls := c.LastRun("ghost"); ls != nil {
		t.Error("runs for unknown jobs must be pruned at load")
	}
}
