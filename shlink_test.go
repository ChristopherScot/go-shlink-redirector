package main

import (
	"encoding/json"
	"io"
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
		shlinkAPI:    apiURL,
		shlinkAPIKey: "test-key",
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

func TestCreateShortURL_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var in struct {
			LongURL    string `json:"longUrl"`
			CustomSlug string `json:"customSlug"`
		}
		json.Unmarshal(body, &in)
		if in.CustomSlug != "gcp" || in.LongURL != "https://console.cloud.google.com/" {
			t.Errorf("bad body: %s", string(body))
		}
		json.NewEncoder(w).Encode(map[string]string{"shortUrl": "https://go/gcp"})
	}))
	defer srv.Close()

	r := newTestResolver(t, srv.URL)
	got, err := r.createShortURL("gcp", "https://console.cloud.google.com/")
	if err != nil {
		t.Fatalf("createShortURL err = %v", err)
	}
	if got != "https://go/gcp" {
		t.Errorf("got %q, want https://go/gcp", got)
	}
}

func TestCreateShortURL_ShlinkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"detail": "slug already exists"})
	}))
	defer srv.Close()

	r := newTestResolver(t, srv.URL)
	_, err := r.createShortURL("gcp", "https://a.example/")
	if err == nil || !strings.Contains(err.Error(), "slug already exists") {
		t.Fatalf("err = %v, want message about slug already existing", err)
	}
}

func TestHandleSlug_Redirect(t *testing.T) {
	shlink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"longUrl": "https://example.com/dst"})
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
	if loc := rec.Header().Get("Location"); loc != "https://example.com/dst" {
		t.Errorf("Location = %q", loc)
	}
}

func TestHandleSlug_MissRendersForm(t *testing.T) {
	shlink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer shlink.Close()

	r := newTestResolver(t, shlink.URL)
	req := httptest.NewRequest("GET", "/gcp", nil)
	req = withChiParam(req, "slug", "gcp")
	rec := httptest.NewRecorder()
	r.handleSlug(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "go/gcp") {
		t.Errorf("missing slug in body: %s", body[:min(400, len(body))])
	}
	if !strings.Contains(body, "doesn") {
		t.Errorf("missing headline in body")
	}
	if !strings.Contains(body, `action="/gcp"`) {
		t.Errorf("form action wrong, body: %s", body[:200])
	}
	if !strings.Contains(body, `name="longUrl"`) {
		t.Errorf("longUrl input missing")
	}
}

func TestHandleCreate_Success(t *testing.T) {
	shlink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"shortUrl": "https://go/gcp"})
	}))
	defer shlink.Close()

	r := newTestResolver(t, shlink.URL)
	form := url.Values{}
	form.Set("longUrl", "https://console.cloud.google.com/")
	req := httptest.NewRequest("POST", "/gcp", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withChiParam(req, "slug", "gcp")
	rec := httptest.NewRecorder()
	r.handleCreate(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/gcp" {
		t.Errorf("Location = %q, want /gcp", loc)
	}
}

func TestHandleCreate_MissingURL(t *testing.T) {
	r := newTestResolver(t, "http://ignored")
	req := httptest.NewRequest("POST", "/gcp", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withChiParam(req, "slug", "gcp")
	rec := httptest.NewRecorder()
	r.handleCreate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCreate_SchemeHandling(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"example.com/path", "http://example.com/path"},
		{"http://x.example/y", "http://x.example/y"},
		{"https://x.example/y", "https://x.example/y"},
		{"mailto:foo@x.example", "mailto:foo@x.example"},
		{"tel:+15551234", "tel:+15551234"},
		{"slack://channel?team=T", "slack://channel?team=T"},
		{"obsidian://open?vault=N", "obsidian://open?vault=N"},
		{"magnet:?xt=urn:btih:abc", "magnet:?xt=urn:btih:abc"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			var got string
			shlink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var in struct {
					LongURL string `json:"longUrl"`
				}
				json.Unmarshal(body, &in)
				got = in.LongURL
				json.NewEncoder(w).Encode(map[string]string{"shortUrl": "https://go/x"})
			}))
			defer shlink.Close()

			r := newTestResolver(t, shlink.URL)
			form := url.Values{}
			form.Set("longUrl", c.in)
			req := httptest.NewRequest("POST", "/x", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withChiParam(req, "slug", "x")
			rec := httptest.NewRecorder()
			r.handleCreate(rec, req)
			if got != c.want {
				t.Errorf("longUrl sent to shlink = %q, want %q", got, c.want)
			}
		})
	}
}
