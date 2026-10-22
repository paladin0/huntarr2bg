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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type catalogueConfig struct{ URL, Key string }

var catalogueSettingsMu sync.Mutex
var catalogueConfigPath = "/config/catalogue-settings.json"
var catalogueFields = map[string]string{
	"CATALOGUE_LAZYLIBRARIAN_URL": "url", "CATALOGUE_LAZYLIBRARIAN_KEY": "secret",
	"CATALOGUE_RETROARR_URL": "url", "CATALOGUE_RETROARR_KEY": "secret",
	"MEDIA_HUNT_ENABLED": "bool", "MEDIA_HUNT_INTERVAL_MINUTES": "int",
	"MEDIA_HUNT_BATCH": "int", "MEDIA_HUNT_RETRY_MINUTES": "int",
	"MEDIA_HUNT_RESCAN_HOURS": "int", "MEDIA_HUNT_GAME_AUTO_DISPATCH": "bool",
}

func catalogueReadSaved() (map[string]string, error) {
	b, e := os.ReadFile(catalogueConfigPath)
	if os.IsNotExist(e) {
		return map[string]string{}, nil
	}
	if e != nil {
		return nil, e
	}
	var v map[string]string
	if e = json.Unmarshal(b, &v); e != nil {
		return nil, e
	}
	if v == nil {
		v = map[string]string{}
	}
	return v, nil
}
func catalogueSetting(name string) string {
	catalogueSettingsMu.Lock()
	defer catalogueSettingsMu.Unlock()
	m, e := catalogueReadSaved()
	if e == nil {
		if v, ok := m[name]; ok {
			return v
		}
	}
	return os.Getenv(name)
}
func catalogueEnv(prefix string) catalogueConfig {
	return catalogueConfig{URL: strings.TrimRight(strings.TrimSpace(catalogueSetting(prefix+"_URL")), "/"), Key: strings.TrimSpace(catalogueSetting(prefix + "_KEY"))}
}
func catalogueSettingsAPI(w http.ResponseWriter, r *http.Request) {
	catalogueSettingsMu.Lock()
	defer catalogueSettingsMu.Unlock()
	values, e := catalogueReadSaved()
	if e != nil {
		http.Error(w, "Cannot read integration settings", 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		type field struct {
			Type       string `json:"type"`
			Value      string `json:"value"`
			Configured bool   `json:"configured"`
		}
		fields := map[string]field{}
		for name, kind := range catalogueFields {
			v, ok := values[name]
			if !ok {
				v = os.Getenv(name)
			}
			shown := v
			if kind == "secret" {
				shown = ""
			}
			fields[name] = field{kind, shown, v != ""}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"fields": fields})
		return
	}
	if r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var payload struct {
		Settings map[string]string `json:"settings"`
	}
	if e = json.NewDecoder(r.Body).Decode(&payload); e != nil {
		http.Error(w, "Invalid settings", 400)
		return
	}
	for name, v := range payload.Settings {
		kind, ok := catalogueFields[name]
		if !ok {
			http.Error(w, "Unknown setting", 400)
			return
		}
		v = strings.TrimSpace(v)
		if kind == "secret" && v == "" {
			continue
		}
		if kind == "url" && v != "" {
			u, e := url.Parse(v)
			if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
				http.Error(w, "Invalid URL", 400)
				return
			}
			v = strings.TrimRight(v, "/")
		}
		if kind == "bool" && v != "true" && v != "false" {
			http.Error(w, "Invalid boolean", 400)
			return
		}
		if kind == "int" {
			n, e := strconv.Atoi(v)
			if e != nil || n < 0 || n > 10000 {
				http.Error(w, "Invalid integer", 400)
				return
			}
		}
		values[name] = v
	}
	b, e := json.MarshalIndent(values, "", "  ")
	if e != nil {
		http.Error(w, "Encoding error", 500)
		return
	}
	if e = os.MkdirAll(filepath.Dir(catalogueConfigPath), 0700); e != nil {
		http.Error(w, "Cannot store settings", 500)
		return
	}
	tmp := catalogueConfigPath + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		http.Error(w, "Cannot store settings", 500)
		return
	}
	if e = os.Rename(tmp, catalogueConfigPath); e != nil {
		http.Error(w, "Cannot save settings", 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"saved": true})
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
	mux.HandleFunc("GET /api/catalogue/settings", catalogueSettingsAPI)
	mux.HandleFunc("PUT /api/catalogue/settings", catalogueSettingsAPI)
	for _, kind := range []string{"authors", "books", "games"} {
		k := kind
		mux.HandleFunc("GET /api/catalogue/"+kind, func(w http.ResponseWriter, r *http.Request) { catalogueProxy(w, r, k) })
	}
}
