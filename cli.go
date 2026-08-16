package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
)

const defaultCLIAddr = "http://127.0.0.1:8080"

func RunCLI(args []string) int {
	return runCLIIn(args, os.Stdout)
}

func runCLIIn(args []string, out io.Writer) int {
	if len(args) == 0 {
		usage(out)
		return 1
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(out)
		return 0
	case "list":
		return cliList(args[1:], out)
	case "get":
		return cliGet(args[1:], out)
	case "add":
		return cliAdd(args[1:], out)
	case "update":
		return cliUpdate(args[1:], out)
	case "delete":
		return cliDelete(args[1:], out)
	case "enable":
		return cliSetEnabled(args[1:], out, true)
	case "disable":
		return cliSetEnabled(args[1:], out, false)
	case "run":
		return cliRun(args[1:], out)
	default:
		fmt.Fprintf(out, "unknown command: %s\n", args[0])
		usage(out)
		return 1
	}
}

type cliOpts struct {
	addr  string
	token string
}

func cliFlags(fs *flag.FlagSet, args []string) (cliOpts, error) {
	addr := fs.String("addr", defaultCLIAddr, "API base URL")
	token := fs.String("token", os.Getenv("CRONIX_API_TOKEN"), "API bearer token")
	if err := fs.Parse(args); err != nil {
		return cliOpts{}, err
	}
	return cliOpts{addr: *addr, token: *token}, nil
}

func cliList(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	status, body, err := cliHTTP("GET", opts.addr+"/api/v1/jobs", opts.token, nil)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusOK {
		return cliErr(out, status, body)
	}
	var jobs []Job
	if err := json.Unmarshal(body, &jobs); err != nil {
		fmt.Fprintf(out, "error: cannot decode response: %v\n", err)
		return 2
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tENABLED\tSCHEDULE")
	for _, j := range jobs {
		fmt.Fprintf(w, "%s\t%s\t%v\t%s\n", j.Id, j.Name, j.Enabled, j.Schedule)
	}
	w.Flush()
	return 0
}

func cliGet(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(out, "usage: cronix cli get [--addr URL] [--token TOKEN] <id>")
		return 1
	}
	status, body, err := cliHTTP("GET", opts.addr+"/api/v1/jobs/"+fs.Arg(0), opts.token, nil)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusOK {
		return cliErr(out, status, body)
	}
	out.Write(body)
	fmt.Fprintln(out)
	return 0
}

func cliAdd(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("id", "", "job id (generated if empty)")
	name := fs.String("name", "", "job name")
	schedule := fs.String("schedule", "", "5-field cron expression")
	curlCmd := fs.String("curl", "", "curl command")
	retries := fs.Int("retries", 0, "extra attempts on failure")
	retryDelay := fs.Int("retry-delay", 5, "seconds between retries")
	enabled := fs.Bool("enabled", true, "enable the job")
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if *schedule == "" || *curlCmd == "" {
		fmt.Fprintln(out, "add requires --schedule and --curl")
		return 1
	}
	body := map[string]any{
		"id": *id, "name": *name, "schedule": *schedule, "curl": *curlCmd,
		"retries": *retries, "retry_delay": *retryDelay, "enabled": *enabled,
	}
	status, resp, err := cliHTTP("POST", opts.addr+"/api/v1/jobs", opts.token, body)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusCreated {
		return cliErr(out, status, resp)
	}
	var j Job
	if err := json.Unmarshal(resp, &j); err != nil {
		fmt.Fprintf(out, "error: cannot decode response: %v\n", err)
		return 2
	}
	fmt.Fprintf(out, "created %s\n", j.Id)
	return 0
}

func cliUpdate(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "job name")
	schedule := fs.String("schedule", "", "5-field cron expression")
	curlCmd := fs.String("curl", "", "curl command")
	retries := fs.Int("retries", 0, "extra attempts on failure")
	retryDelay := fs.Int("retry-delay", 5, "seconds between retries")
	enabled := fs.Bool("enabled", true, "enable the job")
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(out, "usage: cronix cli update [--name N] [--schedule S] [--curl C] [--retries N] [--retry-delay S] [--enabled B] <id>")
		return 1
	}
	id := fs.Arg(0)
	job, err := cliGetJob(opts, id)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	// Partial update: only overlay flags the user actually set, then PUT the
	// full replacement so unchanged fields are preserved.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["name"] {
		job.Name = *name
	}
	if set["schedule"] {
		job.Schedule = *schedule
	}
	if set["curl"] {
		job.Curl = *curlCmd
	}
	if set["retries"] {
		job.Retries = *retries
	}
	if set["retry-delay"] {
		job.RetryDelay = *retryDelay
	}
	if set["enabled"] {
		job.Enabled = *enabled
	}
	status, resp, err := cliHTTP("PUT", opts.addr+"/api/v1/jobs/"+id, opts.token, map[string]any{
		"name":        job.Name,
		"schedule":    job.Schedule,
		"curl":        job.Curl,
		"retries":     job.Retries,
		"retry_delay": job.RetryDelay,
		"enabled":     job.Enabled,
	})
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusOK {
		return cliErr(out, status, resp)
	}
	out.Write(resp)
	fmt.Fprintln(out)
	return 0
}

func cliDelete(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(out, "usage: cronix cli delete [--addr URL] [--token TOKEN] <id>")
		return 1
	}
	status, resp, err := cliHTTP("DELETE", opts.addr+"/api/v1/jobs/"+fs.Arg(0), opts.token, nil)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusNoContent {
		return cliErr(out, status, resp)
	}
	fmt.Fprintf(out, "deleted %s\n", fs.Arg(0))
	return 0
}

func cliSetEnabled(args []string, out io.Writer, enabled bool) int {
	cmd := "enable"
	if !enabled {
		cmd = "disable"
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(out, "usage: cronix cli %s [--addr URL] [--token TOKEN] <id>\n", cmd)
		return 1
	}
	id := fs.Arg(0)
	job, err := cliGetJob(opts, id)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	status, resp, err := cliHTTP("PUT", opts.addr+"/api/v1/jobs/"+id, opts.token, map[string]any{
		"name":        job.Name,
		"schedule":    job.Schedule,
		"curl":        job.Curl,
		"retries":     job.Retries,
		"retry_delay": job.RetryDelay,
		"enabled":     enabled,
	})
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusOK {
		return cliErr(out, status, resp)
	}
	fmt.Fprintf(out, "%s %s\n", cmd, id)
	return 0
}

func cliGetJob(opts cliOpts, id string) (Job, error) {
	status, body, err := cliHTTP("GET", opts.addr+"/api/v1/jobs/"+id, opts.token, nil)
	if err != nil {
		return Job{}, err
	}
	if status != http.StatusOK {
		return Job{}, fmt.Errorf("cannot fetch job %s (status %d)", id, status)
	}
	var j Job
	if err := json.Unmarshal(body, &j); err != nil {
		return Job{}, fmt.Errorf("cannot decode job: %w", err)
	}
	return j, nil
}

func cliRun(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts, err := cliFlags(fs, args)
	if err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(out, "usage: cronix cli run [--addr URL] [--token TOKEN] <id>")
		return 1
	}
	id := fs.Arg(0)
	job, err := cliGetJob(opts, id)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	status, resp, err := cliHTTP("POST", opts.addr+"/api/v1/jobs/"+id+"/run", opts.token, nil)
	if err != nil {
		fmt.Fprintf(out, "error: %v\n", err)
		return 2
	}
	if status != http.StatusOK {
		return cliErr(out, status, resp)
	}
	var runResp struct {
		Steps []Result `json:"steps"`
	}
	if err := json.Unmarshal(resp, &runResp); err != nil {
		fmt.Fprintf(out, "error: cannot decode run: %v\n", err)
		return 2
	}
	var runErr error
	if n := len(runResp.Steps); n > 0 && runResp.Steps[n-1].ExitCode != 0 {
		runErr = ErrCommandFailed
	}
	fmt.Fprintln(out, formatRun(job, runResp.Steps, runErr))
	return 0
}

func cliHTTP(method, url, token string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, b, nil
}

func cliErr(out io.Writer, status int, body []byte) int {
	var e map[string]string
	if err := json.Unmarshal(body, &e); err == nil && e["error"] != "" {
		fmt.Fprintf(out, "error: %s\n", e["error"])
	} else {
		fmt.Fprintf(out, "error: status %d\n", status)
	}
	return 2
}

func usage(out io.Writer) {
	fmt.Fprintf(out, `usage: cronix [cli <command>] ...
Flags must precede the positional <id> (Go's flag package stops parsing at the
first non-flag argument), e.g. "get --addr URL --token TOKEN <id>".

Commands:
  list                              list jobs
  get [--addr URL] [--token TOKEN] <id>
                                    show one job
  add ...                           create a job
  update [--name N] [--schedule S] [--curl C] [--retries N] [--retry-delay S] [--enabled B] <id>
                                    replace a job
  delete [--addr URL] [--token TOKEN] <id>
                                    delete a job
  enable [--addr URL] [--token TOKEN] <id>
                                    enable a paused job
  disable [--addr URL] [--token TOKEN] <id>
                                    disable a job
  run [--addr URL] [--token TOKEN] <id>
                                    run a job now

Common flags: --addr URL (default %s)  --token TOKEN (default $CRONIX_API_TOKEN)
add/update flags: --id --name --schedule --curl --retries --retry-delay --enabled
`, defaultCLIAddr)
}
