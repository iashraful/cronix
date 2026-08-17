package model

import (
	"errors"
	"time"
)

var (
	ErrNotFound      = errors.New("job not found")
	ErrValidation    = errors.New("validation failed")
	ErrStorage       = errors.New("storage failed")
	ErrCommandFailed = errors.New("command failed after exhausting retries")
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

type Result struct {
	Attempt  int    `json:"attempt"`
	Total    int    `json:"total"`
	ExitCode int    `json:"exit"`
	Output   string `json:"output"`
}

type Run struct {
	JobId    string    `json:"job_id"`
	Trigger  string    `json:"trigger"`
	Time     time.Time `json:"time"`
	Status   string    `json:"status"`
	ExitCode int       `json:"exit_code"`
	Results  []Result  `json:"results"`
}

type RunSummary struct {
	Status   string    `json:"status"`
	ExitCode int       `json:"exit_code"`
	Time     time.Time `json:"time"`
}
