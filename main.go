package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	defaultStorePath = "/data/jobs.json"
	defaultRunsPath  = "/data/runs.json"
	defaultHTTPAddr  = ":8080"
	defaultCurlPath  = "/usr/local/bin/curl"
)

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "cli" {
		os.Exit(RunCLI(os.Args[2:]))
	}

	token := os.Getenv("CRONIX_API_TOKEN")
	if strings.TrimSpace(token) == "" {
		log.Fatalf("CRONIX_API_TOKEN is required; refusing to start")
	}

	storePath := envOr("CRONIX_STORE_PATH", defaultStorePath)
	runsPath := envOr("CRONIX_RUNS_PATH", defaultRunsPath)
	httpAddr := envOr("CRONIX_HTTP_ADDR", defaultHTTPAddr)
	curlPath := envOr("CURL_PATH", defaultCurlPath)

	ctrl, err := NewController(NewFileStore(storePath), NewFileRunStore(runsPath), curlPath)
	if err != nil {
		log.Fatalf("startup error: %v", err)
	}

	jobs := ctrl.List()
	log.Printf("cronix started with %d job(s)", len(jobs))
	if len(jobs) == 0 {
		log.Printf("no jobs configured; add one with '/cronix cli add' or the web UI at http://%s", httpAddr)
	}
	ctrl.Start()

	srv := &http.Server{Addr: httpAddr, Handler: NewServer(ctrl, token)}
	go func() {
		log.Printf("api listening on %s", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("api server: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	<-ctx.Done()

	log.Printf("shutting down")
	done := ctrl.Stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		log.Printf("in-flight jobs did not finish in 10s; exiting")
	}
	log.Printf("bye")
}
