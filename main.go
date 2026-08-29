package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type config struct {
	shlinkAPI    string
	shlinkAPIKey string
	shortDomain  string // domain shlink prepends to generated short URLs, e.g. "go"
	listenAddr   string
}

func loadConfig() (config, error) {
	c := config{
		shlinkAPI:    os.Getenv("SHLINK_API_URL"),
		shlinkAPIKey: os.Getenv("SHLINK_API_KEY"),
		shortDomain:  os.Getenv("SHORT_DOMAIN"),
		listenAddr:   os.Getenv("LISTEN_ADDR"),
	}
	if c.shlinkAPI == "" {
		return c, errors.New("SHLINK_API_URL required")
	}
	if c.shlinkAPIKey == "" {
		return c, errors.New("SHLINK_API_KEY required")
	}
	if c.shortDomain == "" {
		c.shortDomain = "go"
	}
	if c.listenAddr == "" {
		c.listenAddr = "0.0.0.0:3000"
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
	// Give a bare hostname a scheme.
	if !strings.HasPrefix(longURL, "http://") && !strings.HasPrefix(longURL, "https://") {
		longURL = "https://" + longURL
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
	// Proxy shlink's own admin/API/CLI paths straight through so this
	// service can front the same host without shadowing them. Slug lookup
	// is a plain GET /{slug}, so leaving /rest, /api, /cli untouched keeps
	// the shlink dashboard's backend calls and shlink-cli working via
	// go.home.
	shlinkURL, err := url.Parse(cfg.shlinkAPI)
	if err != nil {
		logger.Error("bad SHLINK_API_URL", "err", err.Error())
		os.Exit(1)
	}
	proxy := httputil.NewSingleHostReverseProxy(shlinkURL)
	for _, prefix := range []string{"/rest", "/api", "/cli"} {
		r.Handle(prefix+"/*", proxy)
		r.Handle(prefix, proxy)
	}
	r.Get("/{slug}", res.handleSlug)
	r.Post("/{slug}", res.handleCreate)
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(rootPage))
	})

	logger.Info("listening", "addr", cfg.listenAddr, "shlink_api", cfg.shlinkAPI)
	if err := http.ListenAndServe(cfg.listenAddr, r); err != nil {
		logger.Error("server exited", "err", err.Error())
		os.Exit(1)
	}
}

const createTmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Create go/{{.Slug}}</title>
<style>
:root { color-scheme: light dark; }
body { font: 16px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
       max-width: 34rem; margin: 4rem auto; padding: 0 1rem; }
h1 { font-size: 1.4rem; margin: 0 0 .25rem; }
p.hint { color: color-mix(in oklab, currentColor 60%, transparent); margin: 0 0 1.5rem; }
form { display: grid; gap: .75rem; }
label { font-weight: 600; font-size: .9rem; }
input[type=url] { padding: .6rem .75rem; font-size: 1rem; border-radius: .5rem;
                  border: 1px solid color-mix(in oklab, currentColor 30%, transparent);
                  background: transparent; color: inherit; }
button { padding: .6rem 1.2rem; font-size: 1rem; font-weight: 600; border: none;
         border-radius: .5rem; background: #2563eb; color: white; cursor: pointer; }
button:hover { background: #1d4ed8; }
.err { color: #dc2626; margin: 0 0 1rem; }
code { background: color-mix(in oklab, currentColor 12%, transparent);
       padding: .1rem .4rem; border-radius: .25rem; }
</style>
</head>
<body>
<h1>go/{{.Slug}} doesn't exist yet</h1>
<p class="hint">Enter a URL to point <code>go/{{.Slug}}</code> at.</p>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form method="post" action="/{{.Slug}}">
  <label for="longUrl">Long URL</label>
  <input id="longUrl" name="longUrl" type="url" placeholder="https://example.com/…" required autofocus>
  <button type="submit">Create</button>
</form>
</body>
</html>
`

const rootPage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>go</title></head>
<body style="font: 16px/1.5 -apple-system, sans-serif; max-width: 30rem; margin: 4rem auto; padding: 0 1rem;">
<h1 style="font-size: 1.4rem;">go/</h1>
<p>Type <code>go/&lt;slug&gt;</code>. Existing slugs redirect. Unknown ones let you create.</p>
</body>
</html>
`
