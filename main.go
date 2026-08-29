package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type config struct {
	shlinkAPI     string
	shlinkAPIKey  string
	webClientURL  string
	webClientPath string
	listenAddr    string
}

func loadConfig() (config, error) {
	c := config{
		shlinkAPI:     os.Getenv("SHLINK_API_URL"),
		shlinkAPIKey:  os.Getenv("SHLINK_API_KEY"),
		webClientURL:  os.Getenv("WEB_CLIENT_URL"),
		webClientPath: os.Getenv("WEB_CLIENT_CREATE_PATH"),
		listenAddr:    os.Getenv("LISTEN_ADDR"),
	}
	if c.shlinkAPI == "" {
		return c, errors.New("SHLINK_API_URL required")
	}
	if c.shlinkAPIKey == "" {
		return c, errors.New("SHLINK_API_KEY required")
	}
	if c.webClientURL == "" {
		return c, errors.New("WEB_CLIENT_URL required")
	}
	if c.webClientPath == "" {
		c.webClientPath = "/server/homelab/create-short-url"
	}
	if c.listenAddr == "" {
		c.listenAddr = "0.0.0.0:3000"
	}
	c.shlinkAPI = strings.TrimRight(c.shlinkAPI, "/")
	c.webClientURL = strings.TrimRight(c.webClientURL, "/")
	return c, nil
}

type resolver struct {
	cfg    config
	client *http.Client
	logger *slog.Logger
}

func newResolver(cfg config, logger *slog.Logger) *resolver {
	return &resolver{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
		logger: logger,
	}
}

// lookupLongURL returns the long URL for a slug, ("", nil) if not found.
func (r *resolver) lookupLongURL(slug string) (string, error) {
	// shlink returns 404 for a missing slug; anything else is a real error.
	u := r.cfg.shlinkAPI + "/rest/v3/short-urls/" + url.PathEscape(slug)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Api-Key", r.cfg.shlinkAPIKey)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var body struct {
			LongURL string `json:"longUrl"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", fmt.Errorf("decode: %w", err)
		}
		if body.LongURL == "" {
			return "", errors.New("shlink returned 200 with empty longUrl")
		}
		return body.LongURL, nil
	case http.StatusNotFound:
		return "", nil
	default:
		return "", fmt.Errorf("shlink returned %d", resp.StatusCode)
	}
}

func (r *resolver) createURL(slug string) string {
	q := url.Values{}
	q.Set("customSlug", slug)
	return r.cfg.webClientURL + r.cfg.webClientPath + "?" + q.Encode()
}

func (r *resolver) handleSlug(w http.ResponseWriter, req *http.Request) {
	slug := chi.URLParam(req, "slug")
	if slug == "" {
		// Bare / hits shlink's own root-redirect handling, not us; be safe.
		http.Redirect(w, req, r.cfg.webClientURL, http.StatusFound)
		return
	}
	long, err := r.lookupLongURL(slug)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "shlink lookup failed",
			"slug", slug, "err", err.Error())
		http.Error(w, "shlink lookup failed", http.StatusBadGateway)
		return
	}
	if long != "" {
		http.Redirect(w, req, long, http.StatusFound)
		return
	}
	// Miss: send them to the prefilled create page.
	// TODO: fuzzy-match against existing slugs and offer suggestions
	// on the create page once the UX is figured out.
	http.Redirect(w, req, r.createURL(slug), http.StatusFound)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("config invalid", "err", err.Error())
		os.Exit(1)
	}
	res := newResolver(cfg, logger)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	r.Get("/{slug}", res.handleSlug)
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, cfg.webClientURL, http.StatusFound)
	})

	logger.Info("listening", "addr", cfg.listenAddr, "shlink_api", cfg.shlinkAPI)
	if err := http.ListenAndServe(cfg.listenAddr, r); err != nil {
		logger.Error("server exited", "err", err.Error())
		os.Exit(1)
	}
}
