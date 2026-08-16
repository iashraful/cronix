package main

import (
	"errors"
	"fmt"
	"strings"
)

func formatRun(job Job, results []Result, err error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "run job=%s", job.Id)
	if job.Name != "" {
		fmt.Fprintf(&b, " name=%s", job.Name)
	}
	fmt.Fprintf(&b, " schedule=%q command=%q\n", job.Schedule, job.Curl)
	for _, r := range results {
		out := strings.TrimSpace(r.Output)
		if out == "" {
			out = "(no output)"
		}
		fmt.Fprintf(&b, "  attempt %d/%d exit=%d\n    output: %s\n", r.Attempt, r.Total, r.ExitCode, out)
	}
	switch {
	case err == nil:
		fmt.Fprintf(&b, "  result: OK")
	case errors.Is(err, ErrCommandFailed):
		lastExit, total := 0, 1
		if len(results) > 0 {
			lastExit = results[len(results)-1].ExitCode
			total = results[len(results)-1].Total
		}
		fmt.Fprintf(&b, "  result: FAILED (last exit=%d, %d/%d attempts)", lastExit, len(results), total)
	default:
		fmt.Fprintf(&b, "  result: ERROR (%v)", err)
	}
	return b.String()
}
