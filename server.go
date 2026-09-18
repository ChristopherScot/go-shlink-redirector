package main

// Everything in this file knows what this service serves. main.go does
// not: it calls handler() and starts whatever comes back.
//
// Scaffolded by `homelabctl init --no-spec`, because OpenAPI cannot
// usefully describe this service: it answers with 302s and HTML forms,
// takes a form POST rather than a JSON body, and reverse-proxies three
// prefixes straight through to shlink. A generated router would reject
// every path it does not name, which is the opposite of what a
// URL shortener needs - any slug is a valid path.
//
// This file is ours to edit. main.go, the Dockerfile, CI and every
// manifest are the template's and stay as generated, so a later
// convention change still reaches this service.

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Request metrics, labelled by the chi ROUTE PATTERN rather than the
// path. This is load-bearing here in a way it is not for most services:
// every short link is its own path, so labelling by path would mint one
// metric series per slug ever visited - unbounded cardinality, and every
// slug anyone ever typed written into the monitoring stack.
//
// The pattern for GET /{slug} is the literal string "/{slug}", so the
// label set is bounded by the routes below.
var (
	requests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Requests by route, method and status.",
	}, []string{"route", "method", "status"})

	latency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Request latency by route and method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})
)

// observe logs and measures every request.
//
// chi fills the route pattern in only after routing, so this reads it
// AFTER next.ServeHTTP rather than before.
func observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// The kubelet hits /health every few seconds; logging that buries
		// real traffic and fills the counters with self-observation.
		if req.URL.Path == "/health" || req.URL.Path == "/metrics" {
			next.ServeHTTP(w, req)
			return
		}

		start := time.Now()
		rec := middleware.NewWrapResponseWriter(w, req.ProtoMajor)
		next.ServeHTTP(rec, req)

		route := chi.RouteContext(req.Context()).RoutePattern()
		if route == "" {
			// An unrouted request. A literal, so a 404-scanning bot
			// cannot write its paths into a metric label.
			route = "unmatched"
		}
		elapsed := time.Since(start)

		requests.WithLabelValues(route, req.Method, strconv.Itoa(rec.Status())).Inc()
		latency.WithLabelValues(route, req.Method).Observe(elapsed.Seconds())

		slog.Info("request",
			"method", req.Method,
			"route", route,
			"status", rec.Status(),
			"duration_ms", elapsed.Milliseconds())
	})
}

func handler() (http.Handler, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	res := newResolver(cfg, slog.Default())

	// Parsed here rather than in loadConfig so a bad URL fails while
	// building the handler - main.go reports that and exits, instead of
	// a nil proxy panicking on the first request.
	shlinkURL, err := url.Parse(cfg.shlinkAPI)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(shlinkURL)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(observe)

	// What the deployment's readiness and liveness probes hit. Nothing
	// generates this: delete it and the pod never becomes ready, with no
	// error naming the cause.
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	// The deployment annotates this pod for scraping here; without this
	// handler the annotation points at a 404.
	r.Handle("/metrics", promhttp.Handler())

	// Shlink's own admin, API and CLI paths pass straight through, so
	// this service can front the same host without shadowing them. Slug
	// lookup is a plain GET /{slug}, so leaving these untouched keeps the
	// shlink dashboard's backend calls and shlink-cli working via go.home.
	for _, prefix := range []string{"/rest", "/api", "/cli"} {
		r.Handle(prefix+"/*", proxy)
		r.Handle(prefix, proxy)
	}

	r.Get("/{slug}", res.handleSlug)
	r.Post("/{slug}", res.handleCreate)
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(rootPage))
	})

	return r, nil
}
