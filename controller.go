package main

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
)

var (
	ErrNotFound   = errors.New("job not found")
	ErrValidation = errors.New("validation failed")
	ErrStorage    = errors.New("storage failed")
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func wrapValidation(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, a...))
}

type Controller struct {
	mu       sync.Mutex
	runMu    sync.Mutex
	store    Store
	cron     *cron.Cron
	jobs     map[string]Job
	order    []string
	entries  map[string]cron.EntryID
	curlPath string
}

func NewController(store Store, curlPath string) (*Controller, error) {
	c := &Controller{
		store:    store,
		cron:     cron.New(),
		jobs:     map[string]Job{},
		entries:  map[string]cron.EntryID{},
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

func (c *Controller) List() []Job {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Job, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.jobs[id])
	}
	return out
}

func (c *Controller) Get(id string) (Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	j, ok := c.jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return j, nil
}

func (c *Controller) Create(start Job) (Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if start.Id == "" {
		start.Id = genID()
	}
	if _, exists := c.jobs[start.Id]; exists {
		return Job{}, wrapValidation("a job with id %q already exists", start.Id)
	}
	if err := c.validate(start); err != nil {
		return Job{}, err
	}
	c.jobs[start.Id] = start
	c.order = append(c.order, start.Id)
	if err := c.register(start); err != nil {
		delete(c.jobs, start.Id)
		c.removeOrder(start.Id)
		return Job{}, err
	}
	if err := c.saveLocked(); err != nil {
		delete(c.jobs, start.Id)
		c.removeOrder(start.Id)
		c.unregister(start.Id)
		return Job{}, err
	}
	log.Printf("job=%s added", start.Id)
	return start, nil
}

func (c *Controller) Update(id string, next Job) (Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	next.Id = cur.Id
	if err := c.validate(next); err != nil {
		return Job{}, err
	}
	prevEnabled := cur.Enabled
	prevSchedule := cur.Schedule
	c.jobs[id] = next
	if prevEnabled != next.Enabled || prevSchedule != next.Schedule {
		c.unregister(id)
		if next.Enabled {
			if err := c.register(next); err != nil {
				c.jobs[id] = cur
				return Job{}, err
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
		return Job{}, err
	}
	log.Printf("job=%s updated", id)
	return next, nil
}

func (c *Controller) Delete(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
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
	log.Printf("job=%s deleted", id)
	return nil
}

func (c *Controller) Enable(id string) error  { return c.setEnabled(id, true) }
func (c *Controller) Disable(id string) error { return c.setEnabled(id, false) }

func (c *Controller) setEnabled(id string, enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.jobs[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
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

func (c *Controller) Run(id string) ([]Result, error) {
	job, ok := c.jobByID(id)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	c.runMu.Lock()
	defer c.runMu.Unlock()
	return Run(job, c.curlPath)
}

func (c *Controller) validate(j Job) error {
	if !idPattern.MatchString(j.Id) {
		return wrapValidation("id must match %s", idPattern.String())
	}
	if _, err := cron.ParseStandard(j.Schedule); err != nil {
		return wrapValidation("schedule %q: %v", j.Schedule, err)
	}
	if j.Curl == "" {
		return wrapValidation("curl: command is required")
	}
	if _, err := ParseCommand(j.Curl); err != nil {
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

func (c *Controller) register(job Job) error {
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
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	return nil
}

func (c *Controller) snapshot() []Job {
	out := make([]Job, 0, len(c.order))
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
	results, err := Run(job, c.curlPath)
	for _, r := range results {
		log.Printf("job=%s schedule=%q attempt=%d/%d exit=%d output=%s",
			job.Id, job.Schedule, r.Attempt, r.Total, r.ExitCode, strings.TrimSpace(r.Output))
	}
	if err != nil {
		log.Printf("job=%s failed: %v", job.Id, err)
	}
}

// jobByID copies the live job under the controller mutex. It is used by both
// the manual run path and scheduled fires so the job's current curl/retries are
// always read at run time.
func (c *Controller) jobByID(id string) (Job, bool) {
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
