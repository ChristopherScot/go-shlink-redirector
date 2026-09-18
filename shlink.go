package main

// Talking to shlink, and the two handlers that do it. server.go wires
// these to routes; main.go knows about none of it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// hasScheme matches an RFC-3986 scheme (letter, then letter/digit/+/-/.)
// followed by a colon. Covers http://, mailto:, tel:, slack://, obsidian://,
// magnet:, etc. — anything with a scheme is left alone.
var hasScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

// config is what this service needs from the environment. The listen
// address is NOT here: main.go owns it, reading PORT the way every
// homelabctl service does.
//
// SHORT_DOMAIN and WEB_CLIENT_URL used to be read here. Neither was ever
// used after loading - WEB_CLIENT_URL was not even read, only documented
// as required - so both are gone rather than carried forward.
type config struct {
	shlinkAPI    string
	shlinkAPIKey string
}

func loadConfig() (config, error) {
	c := config{
		shlinkAPI:    os.Getenv("SHLINK_API_URL"),
		shlinkAPIKey: os.Getenv("SHLINK_API_KEY"),
	}
	if c.shlinkAPI == "" {
		return c, errors.New("SHLINK_API_URL required")
	}
	if c.shlinkAPIKey == "" {
		return c, errors.New("SHLINK_API_KEY required")
	}
	c.shlinkAPI = strings.TrimRight(c.shlinkAPI, "/")
	return c, nil
}

type resolver struct {
	cfg    config
	client *http.Client
	logger *slog.Logger
	tmpl   *template.Template
}

func newResolver(cfg config, logger *slog.Logger) *resolver {
	return &resolver{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
		logger: logger,
		tmpl:   template.Must(template.New("create").Parse(createTmpl)),
	}
}

// lookupLongURL returns the long URL for a slug, ("", nil) if not found.
func (r *resolver) lookupLongURL(slug string) (string, error) {
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

// createShortURL asks shlink to create a short URL. Returns the generated
// shortUrl.
func (r *resolver) createShortURL(slug, longURL string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"longUrl":    longURL,
		"customSlug": slug,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", r.cfg.shlinkAPI+"/rest/v3/short-urls", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Api-Key", r.cfg.shlinkAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Detail string `json:"detail"`
			Title  string `json:"title"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		msg := e.Detail
		if msg == "" {
			msg = e.Title
		}
		if msg == "" {
			msg = fmt.Sprintf("shlink returned %d", resp.StatusCode)
		}
		return "", errors.New(msg)
	}
	var out struct {
		ShortURL string `json:"shortUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	return out.ShortURL, nil
}

type createPageData struct {
	Slug  string
	Error string
}

// handleSlug is the redirect/create surface for GET /{slug}.
func (r *resolver) handleSlug(w http.ResponseWriter, req *http.Request) {
	slug := chi.URLParam(req, "slug")
	if slug == "" {
		http.NotFound(w, req)
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
	// Miss: render the create form scoped to this slug.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	r.tmpl.Execute(w, createPageData{Slug: slug})
}

// handleCreate accepts a form submission from the miss page and asks
// shlink to create the short URL, then redirects to it.
func (r *resolver) handleCreate(w http.ResponseWriter, req *http.Request) {
	slug := chi.URLParam(req, "slug")
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	longURL := strings.TrimSpace(req.PostForm.Get("longUrl"))
	if longURL == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		r.tmpl.Execute(w, createPageData{Slug: slug, Error: "Please enter a URL."})
		return
	}
	// Give a bare hostname a scheme. Default http:// (not https://) so
	// intranet/dev URLs work — if the target actually enforces HTTPS
	// it'll upgrade on its own; the reverse isn't true. Anything that
	// already has a scheme (http, https, mailto, tel, slack, obsidian,
	// magnet, …) is left alone.
	if !hasScheme.MatchString(longURL) {
		longURL = "http://" + longURL
	}
	if _, err := r.createShortURL(slug, longURL); err != nil {
		r.logger.ErrorContext(req.Context(), "create failed",
			"slug", slug, "err", err.Error())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		r.tmpl.Execute(w, createPageData{Slug: slug, Error: err.Error()})
		return
	}
	// Land the user back on the fresh short URL — shlink returns the
	// full shortUrl but we prefer the same host they came in on.
	http.Redirect(w, req, "/"+slug, http.StatusSeeOther)
}
