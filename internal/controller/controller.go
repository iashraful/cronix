package controller

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"cronix/internal/model"
	"cronix/internal/runlog"
	"cronix/internal/runner"
	"cronix/internal/runstore"
	"cronix/internal/store"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

const runHistoryCap = 50

func wrapValidation(format string, a ...any) error {
	return fmt.Errorf("%w: %s", model.ErrValidation, fmt.Sprintf(format, a...))
}

type Controller struct {
	mu       sync.Mutex
	runMu    sync.Mutex
	store    store.Store
	runStore runstore.RunStore
	cron     *cron.Cron
	jobs     map[string]model.Job
	order    []string
	entries  map[string]cron.EntryID
	runs     map[string][]model.Run
	curlPath string
}

func NewController(store store.Store, runStore runstore.RunStore, curlPath string) (*Controller, error) {
	c := &Controller{
		store:    store,
		runStore: runStore,
		cron:     cron.New(),
		jobs:     map[string]model.Job{},
		entries:  map[string]cron.EntryID{},
		runs:     map[string][]model.Run{},
		curlPath: curlPath,
	}
	stored, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("load store: %w", err)
	}
	for _, job := range stored {
		if err := c.validate(job); err != nil {
			return nil, fmt.Errorf("job %q: %w", job.Id, err)
		}
		c.jobs[job.Id] = job
		c.order = append(c.order, job.Id)
		if err := c.register(job); err != nil {
			return nil, fmt.Errorf("job %q: %w", job.Id, err)
		}
	}
	loadedRuns, err := runStore.LoadRuns()
	if err != nil {
		return nil, fmt.Errorf("load run store: %w", err)
	}
	for _, r := range loadedRuns {
		if _, ok := c.jobs[r.JobId]; !ok {
			continue // prune orphaned runs for jobs that no longer exist
		}
		c.runs[r.JobId] = append(c.runs[r.JobId], r)
	}
	return c, nil
}

func (c *Controller) Start() { c.cron.Start() }

// Stop stops the scheduler and returns a channel that closes once running
// jobs have completed. robfig/cron v3.0.1 exposes Stop() as context.Context,
// so bridge it to the <-chan struct{} contract.
func (c *Controller) Stop() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		<-c.cron.Stop().Done()
		close(done)
	}()
	return done
}

func (c *Controller) List() []model.Job {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]model.Job, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.jobs[id])
	}
	return out
}

func (c *Controller) Get(id string) (model.Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	j, ok := c.jobs[id]
	if !ok {
		return model.Job{}, fmt.Errorf("%w: %s", model.ErrNotFound, id)
	}
	return j, nil
}

func (c *Controller) Create(start model.Job) (model.Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if start.Id == "" {
		start.Id = genID()
	}
	if _, exists := c.jobs[start.Id]; exists {
		return model.Job{}, wrapValidation("a job with id %q already exists", start.Id)
	}
	if err := c.validate(start); err != nil {
		return model.Job{}, err
	}
	c.jobs[start.Id] = start
	c.order = append(c.order, start.Id)
	if err := c.register(start); err != nil {
		delete(c.jobs, start.Id)
		c.removeOrder(start.Id)
		return model.Job{}, err
	}
	if err := c.saveLocked(); err != nil {
		delete(c.jobs, start.Id)
		c.removeOrder(start.Id)
		c.unregister(start.Id)
		return model.Job{}, err
	}
	log.Printf("job=%s added", start.Id)
	return start, nil
}

func (c *Controller) Update(id string, next model.Job) (model.Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return model.Job{}, fmt.Errorf("%w: %s", model.ErrNotFound, id)
	}
	next.Id = cur.Id
	if err := c.validate(next); err != nil {
		return model.Job{}, err
	}
	prevEnabled := cur.Enabled
	prevSchedule := cur.Schedule
	c.jobs[id] = next
	if prevEnabled != next.Enabled || prevSchedule != next.Schedule {
		c.unregister(id)
		if next.Enabled {
			if err := c.register(next); err != nil {
				c.jobs[id] = cur
				return model.Job{}, err
			}
		}
	}
	if err := c.saveLocked(); err != nil {
		// roll back to the original job and its cron registration
		c.jobs[id] = cur
		c.unregister(id)
		if cur.Enabled {
			if err := c.register(cur); err != nil {
				log.Printf("rollback: re-register %s: %v", id, err)
			}
		}
		return model.Job{}, err
	}
	log.Printf("job=%s updated", id)
	return next, nil
}

func (c *Controller) Delete(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return fmt.Errorf("%w: %s", model.ErrNotFound, id)
	}
	pos := c.orderIndex(id)
	delete(c.jobs, id)
	c.removeOrder(id)
	c.unregister(id)
	if err := c.saveLocked(); err != nil {
		// roll back: restore the job, its list position, and registration
		c.jobs[id] = cur
		c.order = append(c.order[:pos], append([]string{id}, c.order[pos:]...)...)
		if err := c.register(cur); err != nil {
			log.Printf("rollback: re-register %s: %v", id, err)
		}
		return err
	}
	delete(c.runs, id)
	if err := c.runStore.SaveRuns(c.runsSnapshot()); err != nil {
		log.Printf("run history save: %v", err)
	}
	log.Printf("job=%s deleted", id)
	return nil
}

func (c *Controller) Enable(id string) error  { return c.setEnabled(id, true) }
func (c *Controller) Disable(id string) error { return c.setEnabled(id, false) }

func (c *Controller) recordRun(job model.Job, trigger string, results []model.Result, runErr error) {
	status := "ok"
	switch {
	case runErr == nil:
	case errors.Is(runErr, model.ErrCommandFailed):
		status = "failed"
	default:
		status = "error"
	}
	exit := 0
	if n := len(results); n > 0 {
		exit = results[n-1].ExitCode
	}
	run := model.Run{
		JobId:    job.Id,
		Trigger:  trigger,
		Time:     time.Now().UTC(),
		Status:   status,
		ExitCode: exit,
		Results:  results,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.jobs[job.Id]; !ok {
		return
	}
	c.runs[job.Id] = append([]model.Run{run}, c.runs[job.Id]...)
	if len(c.runs[job.Id]) > runHistoryCap {
		c.runs[job.Id] = c.runs[job.Id][:runHistoryCap]
	}
	if err := c.runStore.SaveRuns(c.runsSnapshot()); err != nil {
		log.Printf("run history save: %v", err)
	}
}

func (c *Controller) History(id string) ([]model.Run, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.jobs[id]; !ok {
		return nil, fmt.Errorf("%w: %s", model.ErrNotFound, id)
	}
	out := make([]model.Run, len(c.runs[id]))
	copy(out, c.runs[id])
	return out, nil
}

func (c *Controller) LastRun(id string) *model.RunSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	runs := c.runs[id]
	if len(runs) == 0 {
		return nil
	}
	first := runs[0]
	return &model.RunSummary{Status: first.Status, ExitCode: first.ExitCode, Time: first.Time}
}

func (c *Controller) NextRun(id string) (*time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	job, ok := c.jobs[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", model.ErrNotFound, id)
	}
	if !job.Enabled {
		return nil, nil
	}
	sched, err := cron.ParseStandard(job.Schedule)
	if err != nil {
		return nil, nil
	}
	next := sched.Next(time.Now())
	return &next, nil
}

func (c *Controller) runsSnapshot() []model.Run {
	out := make([]model.Run, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.runs[id]...)
	}
	return out
}

func (c *Controller) setEnabled(id string, enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return fmt.Errorf("%w: %s", model.ErrNotFound, id)
	}
	before := cur
	cur.Enabled = enabled
	c.jobs[id] = cur
	c.unregister(id)
	if enabled {
		if err := c.register(cur); err != nil {
			return err
		}
	}
	if err := c.saveLocked(); err != nil {
		// roll back to the original enabled state
		c.jobs[id] = before
		c.unregister(id)
		if before.Enabled {
			if err := c.register(before); err != nil {
				log.Printf("rollback: re-register %s: %v", id, err)
			}
		}
		return err
	}
	action := "disabled"
	if enabled {
		action = "enabled"
	}
	log.Printf("job=%s %s", id, action)
	return nil
}

func (c *Controller) Run(id string) ([]model.Result, error) {
	job, ok := c.jobByID(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", model.ErrNotFound, id)
	}
	c.runMu.Lock()
	defer c.runMu.Unlock()
	results, err := runner.Run(job, c.curlPath)
	c.recordRun(job, "manual", results, err)
	return results, err
}

func (c *Controller) validate(j model.Job) error {
	if !idPattern.MatchString(j.Id) {
		return wrapValidation("id must match %s", idPattern.String())
	}
	if _, err := cron.ParseStandard(j.Schedule); err != nil {
		return wrapValidation("schedule %q: %v", j.Schedule, err)
	}
	if j.Curl == "" {
		return wrapValidation("curl: command is required")
	}
	if _, err := runner.ParseCommand(j.Curl); err != nil {
		return wrapValidation("curl: %v", err)
	}
	if j.Retries < 0 {
		return wrapValidation("retries must be >= 0")
	}
	if j.RetryDelay < 0 {
		return wrapValidation("retry_delay must be >= 0")
	}
	return nil
}

func (c *Controller) register(job model.Job) error {
	if !job.Enabled {
		return nil
	}
	entryID, err := c.cron.AddFunc(job.Schedule, func() {
		c.fire(job.Id)
	})
	if err != nil {
		return err
	}
	c.entries[job.Id] = entryID
	return nil
}

func (c *Controller) unregister(id string) {
	if entryID, ok := c.entries[id]; ok {
		c.cron.Remove(entryID)
		delete(c.entries, id)
	}
}

func (c *Controller) orderIndex(id string) int {
	for i, cur := range c.order {
		if cur == id {
			return i
		}
	}
	return -1
}

func (c *Controller) removeOrder(id string) {
	if i := c.orderIndex(id); i >= 0 {
		c.order = append(c.order[:i], c.order[i+1:]...)
	}
}

func (c *Controller) saveLocked() error {
	if err := c.store.Save(c.snapshot()); err != nil {
		return fmt.Errorf("%w: %v", model.ErrStorage, err)
	}
	return nil
}

func (c *Controller) snapshot() []model.Job {
	out := make([]model.Job, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.jobs[id])
	}
	return out
}

func (c *Controller) fire(id string) {
	job, ok := c.jobByID(id)
	if !ok {
		return
	}
	c.runMu.Lock()
	defer c.runMu.Unlock()
	results, err := runner.Run(job, c.curlPath)
	c.recordRun(job, "scheduled", results, err)
	if err != nil {
		log.Printf("%s", runlog.FormatRun(job, results, err))
		return
	}
	for _, r := range results {
		log.Printf("job=%s name=%s schedule=%q attempt=%d/%d exit=%d output=%s",
			job.Id, job.Name, job.Schedule, r.Attempt, r.Total, r.ExitCode, strings.TrimSpace(r.Output))
	}
}

// jobByID copies the live job under the controller mutex. It is used by both
// the manual run path and scheduled fires so the job's current curl/retries are
// always read at run time.
func (c *Controller) jobByID(id string) (model.Job, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	job, ok := c.jobs[id]
	return job, ok
}

func genID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("job%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
