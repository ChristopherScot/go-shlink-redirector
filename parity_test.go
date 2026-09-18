package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Behaviour parity with the service this replaces, checked against a
// stub shlink rather than against prose.
func TestParityWithTheRunningService(t *testing.T) {
	shlink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Lookups first: they also live under /rest, so ordering here
		// is what separates a slug lookup from a proxied request.
		switch {
		case r.URL.Path == "/rest/v3/short-urls/known":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"longUrl":"https://example.com/target"}`))
		case r.URL.Path == "/rest/v3/short-urls/missing":
			http.Error(w, "not found", http.StatusNotFound)
		case strings.HasPrefix(r.URL.Path, "/rest/"), strings.HasPrefix(r.URL.Path, "/api/"):
			w.WriteHeader(http.StatusTeapot) // proves the proxy reached shlink
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer shlink.Close()

	t.Setenv("SHLINK_API_URL", shlink.URL)
	t.Setenv("SHLINK_API_KEY", "k")
	h, err := handler()
	if err != nil {
		t.Fatal(err)
	}

	do := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}

	// A known slug 302s to the long URL.
	if w := do("GET", "/known"); w.Code != http.StatusFound ||
		w.Header().Get("Location") != "https://example.com/target" {
		t.Errorf("known slug: %d -> %q, want 302 -> the long URL", w.Code, w.Header().Get("Location"))
	}
	// A miss renders the create form (200), matching the RUNNING service -
	// not the 302-to-web-client the old manifest comment described.
	if w := do("GET", "/missing"); w.Code != http.StatusOK {
		t.Errorf("miss: status = %d, want 200 with the create form", w.Code)
	}
	// Shlink's own paths proxy through untouched.
	for _, p := range []string{"/rest/v3/short-urls", "/api/anything"} {
		if w := do("GET", p); w.Code != http.StatusTeapot {
			t.Errorf("%s: status = %d, want the proxied 418", p, w.Code)
		}
	}
	if w := do("GET", "/health"); w.Code != http.StatusOK {
		t.Errorf("/health: status = %d, want 200", w.Code)
	}
}
