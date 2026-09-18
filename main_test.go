package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newHandler builds the real handler against a stub shlink, so the
// routing, the probe endpoint and the metric labels are all exercised
// the way they are in the cluster.
func newHandler(t *testing.T) http.Handler {
	t.Helper()
	shlink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(shlink.Close)

	t.Setenv("SHLINK_API_URL", shlink.URL)
	t.Setenv("SHLINK_API_KEY", "test-key")

	h, err := handler()
	if err != nil {
		t.Fatalf("handler() = %v", err)
	}
	return h
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// The path deploy/deployment.yaml's readiness and liveness probes hit.
// Nothing generates this endpoint - server.go is hand-written - so this
// test is what keeps an edit from removing the thing the probes depend
// on. Without it the pod never becomes ready and nothing says why.
func TestHealthIsServedForTheProbe(t *testing.T) {
	w := get(t, newHandler(t), "/health")
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Errorf("body = %q, want it to report ok", w.Body.String())
	}
}

func TestRootServesTheLandingPage(t *testing.T) {
	w := get(t, newHandler(t), "/")
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

// A slug with no short URL renders the create form rather than 404ing.
// That is the whole interaction: visit go/thing, get offered the chance
// to make it.
func TestUnknownSlugOffersToCreateIt(t *testing.T) {
	w := get(t, newHandler(t), "/does-not-exist")
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "does-not-exist") {
		t.Errorf("create form does not mention the slug:\n%s", w.Body.String())
	}
}

func TestMetricsAreExposed(t *testing.T) {
	h := newHandler(t)
	get(t, h, "/") // a request to record

	w := get(t, h, "/metrics")
	if w.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, `http_requests_total{method="GET",route="/"`) {
		t.Errorf("no request counter for the root route:\n%s", body)
	}
	if !strings.Contains(body, "go_goroutines") {
		t.Error("no runtime metrics in /metrics")
	}
}

// The cardinality guard, and the reason it matters more here than in a
// typical service: every short link is its own path. If the metric label
// were the path rather than the route pattern, Prometheus would gain a
// series per slug anyone ever visited - and every slug would be written
// into the monitoring stack.
func TestSlugNeverBecomesAMetricLabel(t *testing.T) {
	h := newHandler(t)
	get(t, h, "/secret-project-codename")
	get(t, h, "/another-private-slug")

	body := get(t, h, "/metrics").Body.String()
	for _, slug := range []string{"secret-project-codename", "another-private-slug"} {
		if strings.Contains(body, slug) {
			t.Errorf("slug %q reached a metric label", slug)
		}
	}
	// The pattern, not the value.
	if !strings.Contains(body, `route="/{slug}"`) {
		t.Errorf("slug requests were not counted under their route pattern:\n%s", body)
	}
}

// An unrouted path must not become a label either - a 404-scanning bot
// would otherwise write its wordlist into the metrics.
func TestUnroutedPathIsLabelledAsUnmatched(t *testing.T) {
	h := newHandler(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/wp-admin/evil.php", nil))

	body := get(t, h, "/metrics").Body.String()
	if strings.Contains(body, "wp-admin") {
		t.Error("an unrouted path reached a metric label")
	}
}
