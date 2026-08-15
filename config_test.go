package main

import (
	"strings"
	"testing"
)

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestLoadJobsParsesPairs(t *testing.T) {
	m := map[string]string{
		"CRON_SCHEDULE_0": "*/5 * * * *",
		"CRON_CURL_0":     "curl -s https://example.com",
		"CRON_SCHEDULE_1": "* * * * *",
		"CRON_CURL_1":     "curl -X POST https://example.com/api",
	}
	jobs, err := LoadJobs(lookup(m))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Id != "0" || jobs[0].Schedule != "*/5 * * * *" || jobs[0].Curl != "curl -s https://example.com" || !jobs[0].Enabled {
		t.Errorf("job 0 wrong: %+v", jobs[0])
	}
	if jobs[1].Id != "1" || jobs[1].Schedule != "* * * * *" {
		t.Errorf("job 1 wrong: %+v", jobs[1])
	}
}

func TestLoadJobsStopsAtGap(t *testing.T) {
	m := map[string]string{
		"CRON_SCHEDULE_0": "* * * * *",
		"CRON_CURL_0":     "curl -s https://example.com",
		"CRON_SCHEDULE_2": "* * * * *",
		"CRON_CURL_2":     "curl -s https://example.com/2",
	}
	jobs, err := LoadJobs(lookup(m))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job (scan stops at gap), got %d", len(jobs))
	}
}

func TestLoadJobsMissingCurlIsError(t *testing.T) {
	m := map[string]string{"CRON_SCHEDULE_0": "* * * * *"}
	_, err := LoadJobs(lookup(m))
	if err == nil {
		t.Fatal("want error for schedule without curl")
	}
	if !strings.Contains(err.Error(), "CRON_CURL_0") {
		t.Errorf("error should name CRON_CURL_0: %v", err)
	}
}

func TestLoadJobsInvalidScheduleIsError(t *testing.T) {
	m := map[string]string{
		"CRON_SCHEDULE_0": "not a cron",
		"CRON_CURL_0":     "curl -s https://example.com",
	}
	_, err := LoadJobs(lookup(m))
	if err == nil {
		t.Fatal("want error for invalid cron expression")
	}
	if !strings.Contains(err.Error(), "CRON_SCHEDULE_0") {
		t.Errorf("error should name CRON_SCHEDULE_0: %v", err)
	}
}

func TestLoadJobsRetryDefaults(t *testing.T) {
	m := map[string]string{
		"CRON_SCHEDULE_0": "* * * * *",
		"CRON_CURL_0":     "curl -s https://example.com",
	}
	jobs, err := LoadJobs(lookup(m))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jobs[0].Retries != 0 {
		t.Errorf("default retries want 0, got %d", jobs[0].Retries)
	}
	if jobs[0].RetryDelay != 5 {
		t.Errorf("default retry delay want 5, got %d", jobs[0].RetryDelay)
	}
}

func TestLoadJobsRetryParsing(t *testing.T) {
	m := map[string]string{
		"CRON_SCHEDULE_0":    "* * * * *",
		"CRON_CURL_0":        "curl -s https://example.com",
		"CRON_RETRIES_0":     "3",
		"CRON_RETRY_DELAY_0": "10",
	}
	jobs, err := LoadJobs(lookup(m))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jobs[0].Retries != 3 {
		t.Errorf("retries want 3, got %d", jobs[0].Retries)
	}
	if jobs[0].RetryDelay != 10 {
		t.Errorf("retry delay want 10, got %d", jobs[0].RetryDelay)
	}
}

func TestLoadJobsInvalidRetryValues(t *testing.T) {
	for _, tc := range []struct {
		name     string
		retries  string
		delay    string
		wantPart string
	}{
		{"non-numeric retries", "abc", "", "CRON_RETRIES_0"},
		{"negative retries", "-1", "", "CRON_RETRIES_0"},
		{"non-numeric delay", "", "xyz", "CRON_RETRY_DELAY_0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := map[string]string{
				"CRON_SCHEDULE_0": "* * * * *",
				"CRON_CURL_0":     "curl -s https://example.com",
			}
			if tc.retries != "" {
				m["CRON_RETRIES_0"] = tc.retries
			}
			if tc.delay != "" {
				m["CRON_RETRY_DELAY_0"] = tc.delay
			}
			_, err := LoadJobs(lookup(m))
			if err == nil {
				t.Fatal("want error")
			}
			if !strings.Contains(err.Error(), tc.wantPart) {
				t.Errorf("error should mention %s: %v", tc.wantPart, err)
			}
		})
	}
}
