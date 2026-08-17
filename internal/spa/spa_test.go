package spa

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSpaServesIndex(t *testing.T) {
	h := SPAHandler()
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
	h := SPAHandler()
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

func TestSpaServesIndexForClientRoute(t *testing.T) {
	h := SPAHandler()
	for _, path := range []string{"/jobs", "/jobs/abc123", "/runs/xyz"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("GET %s: want 200, got %d", path, w.Code)
		}
		ct := w.Header().Get("Content-Type")
		if !strings.Contains(ct, "text/html") {
			t.Errorf("GET %s: want html content type, got %q", path, ct)
		}
	}
}

func TestSpaServesAsset(t *testing.T) {
	h := SPAHandler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	b, _ := io.ReadAll(w.Result().Body)
	// src looks like "/assets/index-*.js" — derive the path robustly:
	idx := strings.Index(string(b), "/assets/")
	if idx < 0 {
		t.Fatalf("index should reference an asset: %s", b)
	}
	asset := string(b)[idx:]
	asset = asset[:strings.IndexByte(asset, '"')]
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", asset, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: want 200, got %d", asset, w.Code)
	}
}
