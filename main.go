package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
)

func envLookup(name string) (string, bool) {
	return os.LookupEnv(name)
}

func main() {
	curlPath := os.Getenv("CURL_PATH")
	if curlPath == "" {
		curlPath = "/usr/local/bin/curl"
	}

	jobs, err := LoadJobs(envLookup)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	if len(jobs) == 0 {
		log.Fatalf("no jobs configured: set CRON_SCHEDULE_0 and CRON_CURL_0")
	}
	for _, job := range jobs {
		if _, err := ParseCommand(job.Curl); err != nil {
			log.Fatalf("job %s: %v", job.Id, err)
		}
	}

	c := cron.New()
	for _, job := range jobs {
		j := job
		if _, err := c.AddFunc(j.Schedule, func() {
			results, err := Run(j, curlPath)
			for _, r := range results {
				log.Printf("job=%s schedule=%q attempt=%d/%d exit=%d output=%s",
					j.Id, j.Schedule, r.Attempt, r.Total, r.ExitCode, strings.TrimSpace(r.Output))
			}
			if err != nil {
				log.Printf("job=%s failed: %v", j.Id, err)
			}
		}); err != nil {
			log.Fatalf("job %s: %v", j.Id, err)
		}
	}

	log.Printf("cronix started with %d job(s)", len(jobs))
	c.Start()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	<-ctx.Done()

	log.Printf("shutting down")
	done := c.Stop()
	select {
	case <-done.Done():
	case <-time.After(10 * time.Second):
		log.Printf("in-flight jobs did not finish in 10s; exiting")
	}
	log.Printf("bye")
}
