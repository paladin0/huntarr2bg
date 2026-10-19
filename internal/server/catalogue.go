package server

// External library previews are intentionally read-only. They are separate
// from the native ARR scheduler because neither application exposes the ARR
// quality-profile and history semantics the scheduler depends upon.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type catalogueConfig struct{ URL, Key string }

func catalogueEnv(prefix string) catalogueConfig {
	return catalogueConfig{URL: strings.TrimRight(strings.TrimSpace(os.Getenv(prefix+"_URL")), "/"), Key: strings.TrimSpace(os.Getenv(prefix + "_KEY"))}
}

func catalogueProxy(w http.ResponseWriter, r *http.Request, kind string) {
	prefix := "CATALOGUE_LAZYLIBRARIAN"
	if kind == "games" {
		prefix = "CATALOGUE_RETROARR"
	}
	cfg := catalogueEnv(prefix)
	if cfg.URL == "" || cfg.Key == "" {
		http.Error(w, kind+" integration not configured", http.StatusServiceUnavailable)
		return
	}
	base, err := url.Parse(cfg.URL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" {
		http.Error(w, "invalid upstream configuration", http.StatusBadRequest)
		return
	}
	path := "/api/v3/game"
	query := url.Values{}
	if kind == "authors" {
		path = "/api"
		query.Set("cmd", "getIndex")
		query.Set("apikey", cfg.Key)
	}
	if kind == "books" {
		path = "/api"
		query.Set("cmd", "getAllBooks")
		query.Set("apikey", cfg.Key)
	}
	// Whitelisted paths and commands only. No user-supplied upstream URL.
	base.Path = strings.TrimRight(base.Path, "/") + path
	base.RawQuery = query.Encode()
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		http.Error(w, "invalid request", 500)
		return
	}
	if kind == "games" {
		req.Header.Set("X-Api-Key", cfg.Key)
	}
	client := &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, fmt.Sprintf("upstream returned %d", resp.StatusCode), http.StatusBadGateway)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	if err != nil || len(body) > 8*1024*1024 {
		http.Error(w, "upstream response too large", http.StatusBadGateway)
		return
	}
	if !json.Valid(body) {
		http.Error(w, "upstream did not return JSON", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

func (s *Server) registerCatalogueRoutes(mux *http.ServeMux) {
	for _, kind := range []string{"authors", "books", "games"} {
		k := kind
		mux.HandleFunc("GET /api/catalogue/"+kind, func(w http.ResponseWriter, r *http.Request) { catalogueProxy(w, r, k) })
	}
}
