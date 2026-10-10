package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/google/shlex"

	"cronix/internal/model"
)

const outputCap = 4096

var DefaultTimeout = 60 * time.Second

func init() {
	if v := os.Getenv("CRONIX_JOB_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			DefaultTimeout = d
		}
	}
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

func Run(job model.Job, curlPath string) ([]model.Result, error) {
	return RunWithTimeout(job, curlPath, DefaultTimeout)
}

func RunWithTimeout(job model.Job, curlPath string, timeout time.Duration) ([]model.Result, error) {
	return RunContext(context.Background(), job, curlPath, timeout)
}

func RunContext(ctx context.Context, job model.Job, curlPath string, timeout time.Duration) ([]model.Result, error) {
	args, err := ParseCommand(job.Curl)
	if err != nil {
		return nil, err
	}
	total := job.Retries + 1
	results := make([]model.Result, 0, total)
	var lastExit int
	for attempt := 1; attempt <= total; attempt++ {
		attemptCtx := ctx
		var cancel context.CancelFunc
		if timeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		var buf bytes.Buffer
		cmd := exec.CommandContext(attemptCtx, curlPath, args[1:]...)
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		runErr := cmd.Run()
		if cancel != nil {
			cancel()
		}
		exit := 0
		if runErr != nil {
			if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
				exit = 124
				if buf.Len() == 0 {
					buf.WriteString(fmt.Sprintf("command timed out after %v", timeout))
				}
			} else {
				var ee *exec.ExitError
				if errors.As(runErr, &ee) {
					exit = ee.ExitCode()
				} else {
					return nil, fmt.Errorf("cannot start %s: %w", curlPath, runErr)
				}
			}
		}
		lastExit = exit
		results = append(results, model.Result{
			Attempt:  attempt,
			Total:    total,
			ExitCode: exit,
			Output:   tail(buf.Bytes(), outputCap),
		})
		if exit == 0 {
			return results, nil
		}
		if attempt < total {
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(time.Duration(job.RetryDelay) * time.Second):
			}
		}
	}
	return results, fmt.Errorf("%w: job %s failed after %d attempts (last exit=%d)", model.ErrCommandFailed, job.Id, total, lastExit)
}
