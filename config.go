package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/robfig/cron/v3"
)

type Job struct {
	Index      int
	Schedule   string
	Curl       string
	Retries    int
	RetryDelay time.Duration
}

func LoadJobs(lookup func(string) (string, bool)) ([]Job, error) {
	var jobs []Job
	for i := 0; ; i++ {
		schedName := "CRON_SCHEDULE_" + strconv.Itoa(i)
		schedule, ok := lookup(schedName)
		if !ok {
			break
		}
		if _, err := cron.ParseStandard(schedule); err != nil {
			return nil, fmt.Errorf("%s: invalid cron expression %q: %w", schedName, schedule, err)
		}
		curlName := "CRON_CURL_" + strconv.Itoa(i)
		cmd, ok := lookup(curlName)
		if !ok {
			return nil, fmt.Errorf("missing %s for %s", curlName, schedName)
		}
		retries, err := nonNegativeIntEnv(lookup, "CRON_RETRIES_"+strconv.Itoa(i), 0)
		if err != nil {
			return nil, err
		}
		delaySec, err := nonNegativeIntEnv(lookup, "CRON_RETRY_DELAY_"+strconv.Itoa(i), 5)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, Job{
			Index:      i,
			Schedule:   schedule,
			Curl:       cmd,
			Retries:    retries,
			RetryDelay: time.Duration(delaySec) * time.Second,
		})
	}
	return jobs, nil
}

func nonNegativeIntEnv(lookup func(string) (string, bool), name string, def int) (int, error) {
	v, ok := lookup(name)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s: must be a non-negative integer, got %q", name, v)
	}
	return n, nil
}