package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func newTestResolver(t *testing.T, apiURL string) *resolver {
	t.Helper()
	cfg := config{
		shlinkAPI:     apiURL,
		shlinkAPIKey:  "test-key",
		webClientURL:  "https://shlink.home.example.com",
		webClientPath: "/server/homelab/create-short-url",
		listenAddr:    "0.0.0.0:3000",
	}
	return newResolver(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func TestLookupLongURL_Found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/rest/v3/short-urls/gcp"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if r.Header.Get("X-Api-Key") != "test-key" {
			t.Errorf("missing api key header")
		}
		json.NewEncoder(w).Encode(map[string]string{"longUrl": "https://console.cloud.google.com/"})
	}))
	defer srv.Close()

	r := newTestResolver(t, srv.URL)
	got, err := r.lookupLongURL("gcp")
	if err != nil {
		t.Fatalf("lookupLongURL err = %v", err)
	}
	if want := "https://console.cloud.google.com/"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLookupLongURL_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := newTestResolver(t, srv.URL)
	got, err := r.lookupLongURL("nope")
	if err != nil {
		t.Fatalf("lookupLongURL err = %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestLookupLongURL_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := newTestResolver(t, srv.URL)
	if _, err := r.lookupLongURL("boom"); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestCreateURL(t *testing.T) {
	r := newTestResolver(t, "http://ignored")
	got := r.createURL("hello world")
	want := "https://shlink.home.example.com/server/homelab/create-short-url?customSlug=hello+world"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHandleSlug_Redirect(t *testing.T) {
	shlink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"longUrl": "https://example.com/dst"})
	}))
	defer shlink.Close()

	r := newTestResolver(t, shlink.URL)
	req := httptest.NewRequest("GET", "/gcp", nil)
	// simulate chi routing
	req = withChiParam(req, "slug", "gcp")
	rec := httptest.NewRecorder()
	r.handleSlug(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://example.com/dst" {
		t.Errorf("Location = %q", loc)
	}
}

func TestHandleSlug_MissRedirectsToCreate(t *testing.T) {
	shlink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer shlink.Close()

	r := newTestResolver(t, shlink.URL)
	req := httptest.NewRequest("GET", "/gcp", nil)
	req = withChiParam(req, "slug", "gcp")
	rec := httptest.NewRecorder()
	r.handleSlug(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("Location not a URL: %v", err)
	}
	if got := u.Query().Get("customSlug"); got != "gcp" {
		t.Errorf("customSlug = %q, want gcp", got)
	}
	if !strings.HasPrefix(loc, "https://shlink.home.example.com/server/homelab/create-short-url") {
		t.Errorf("bad prefix: %q", loc)
	}
}
