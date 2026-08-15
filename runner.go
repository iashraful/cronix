package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/google/shlex"
)

const outputCap = 4096

// ErrCommandFailed marks a domain outcome: the binary started but the final
// attempt exited non-zero. It is distinct from server faults (unparseable
// command, binary missing) which stay ordinary errors.
var ErrCommandFailed = errors.New("command failed after exhausting retries")

type Result struct {
	Attempt  int    `json:"attempt"`
	Total    int    `json:"total"`
	ExitCode int    `json:"exit"`
	Output   string `json:"output"`
}

func ParseCommand(cmd string) ([]string, error) {
	args, err := shlex.Split(cmd)
	if err != nil {
		return nil, fmt.Errorf("cannot parse command %q: %w", cmd, err)
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command %q", cmd)
	}
	if args[0] != "curl" {
		return nil, fmt.Errorf("command must start with curl, got %q", args[0])
	}
	return args, nil
}

func tail(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[len(b)-max:])
}

func Run(job Job, curlPath string) ([]Result, error) {
	args, err := ParseCommand(job.Curl)
	if err != nil {
		return nil, err
	}
	total := job.Retries + 1
	results := make([]Result, 0, total)
	var lastExit int
	for attempt := 1; attempt <= total; attempt++ {
		var buf bytes.Buffer
		cmd := exec.Command(curlPath, args[1:]...)
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		runErr := cmd.Run()
		exit := 0
		if runErr != nil {
			var ee *exec.ExitError
			if errors.As(runErr, &ee) {
				exit = ee.ExitCode()
			} else {
				return nil, fmt.Errorf("cannot start %s: %w", curlPath, runErr)
			}
		}
		lastExit = exit
		results = append(results, Result{
			Attempt:  attempt,
			Total:    total,
			ExitCode: exit,
			Output:   tail(buf.Bytes(), outputCap),
		})
		if exit == 0 {
			return results, nil
		}
		if attempt < total {
			time.Sleep(time.Duration(job.RetryDelay) * time.Second)
		}
	}
	return results, fmt.Errorf("%w: job %s failed after %d attempts (last exit=%d)", ErrCommandFailed, job.Id, total, lastExit)
}
