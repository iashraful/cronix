package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
)

type jobRequest struct {
	Id         *string `json:"id"`
	Name       *string `json:"name"`
	Schedule   *string `json:"schedule"`
	Curl       *string `json:"curl"`
	Retries    *int    `json:"retries"`
	RetryDelay *int    `json:"retry_delay"`
	Enabled    *bool   `json:"enabled"`
}

func (r jobRequest) toJob() Job {
	j := Job{Enabled: true}
	if r.Id != nil {
		j.Id = *r.Id
	}
	if r.Name != nil {
		j.Name = *r.Name
	}
	if r.Schedule != nil {
		j.Schedule = *r.Schedule
	}
	if r.Curl != nil {
		j.Curl = *r.Curl
	}
	if r.Retries != nil {
		j.Retries = *r.Retries
	}
	if r.RetryDelay != nil {
		j.RetryDelay = *r.RetryDelay
	} else {
		j.RetryDelay = 5
	}
	if r.Enabled != nil {
		j.Enabled = *r.Enabled
	}
	return j
}

type Server struct {
	ctrl  *Controller
	token string
}

func NewServer(ctrl *Controller, token string) http.Handler {
	s := &Server{ctrl: ctrl, token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/jobs", s.auth(s.handleList))
	mux.HandleFunc("POST /api/v1/jobs", s.auth(s.handleCreate))
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.auth(s.handleGet))
	mux.HandleFunc("PUT /api/v1/jobs/{id}", s.auth(s.handleUpdate))
	mux.HandleFunc("DELETE /api/v1/jobs/{id}", s.auth(s.handleDelete))
	mux.HandleFunc("POST /api/v1/jobs/{id}/run", s.auth(s.handleRun))
	mux.HandleFunc("GET /api/v1/jobs/{id}/runs", s.auth(s.handleRuns))
	mux.Handle("GET /", SPAHandler())
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, prefix) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		got := strings.TrimPrefix(h, prefix)
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

type jobResponse struct {
	Job
	LastRun *RunSummary `json:"last_run,omitempty"`
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	jobs := s.ctrl.List()
	out := make([]jobResponse, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobResponse{Job: j, LastRun: s.ctrl.LastRun(j.Id)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req jobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: %v", err)
		return
	}
	job, err := s.ctrl.Create(req.toJob())
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := s.ctrl.Get(id)
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobResponse{Job: job, LastRun: s.ctrl.LastRun(id)})
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var req jobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: %v", err)
		return
	}
	job, err := s.ctrl.Update(r.PathValue("id"), req.toJob())
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.ctrl.Delete(r.PathValue("id")); err != nil {
		writeCtrlError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, gerr := s.ctrl.Get(id)
	if gerr != nil {
		writeCtrlError(w, gerr)
		return
	}
	results, err := s.ctrl.Run(id)
	if err != nil && !errors.Is(err, ErrCommandFailed) {
		writeCtrlError(w, err)
		return
	}
	log.Printf("%s", formatRun(job, results, err))
	writeJSON(w, http.StatusOK, struct {
		Steps []Result `json:"steps"`
	}{Steps: results})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.ctrl.History(r.PathValue("id"))
	if err != nil {
		writeCtrlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Runs []Run `json:"runs"`
	}{Runs: runs})
}

func writeCtrlError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "%s", err.Error())
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "%s", err.Error())
	case errors.Is(err, ErrStorage):
		writeError(w, http.StatusInternalServerError, "%s", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "%s", err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, format string, a ...any) {
	writeJSON(w, status, map[string]string{"error": fmt.Sprintf(format, a...)})
}
