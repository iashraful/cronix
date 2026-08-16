package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func spaServer(t *testing.T) http.Handler {
	t.Helper()
	c, err := NewController(&memStore{}, &memRunStore{}, "/bin/true")
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return NewServer(c, "secret")
}

func TestSpaServesIndex(t *testing.T) {
	h := spaServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("want html content type, got %q", ct)
	}
}

func TestSpaServesReactIndex(t *testing.T) {
	h := spaServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	b, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(b), `<div id="root">`) {
		t.Error("built index should contain the React mount node")
	}
	if !strings.Contains(string(b), "/assets/") {
		t.Error("built index should reference Vite asset bundles")
	}
}
