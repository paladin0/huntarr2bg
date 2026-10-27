package server

// Media hunting is isolated from the quality-upgrade scheduler: LazyLibrarian
// and RetroArr have different quality and availability semantics to *arr apps.
// The service persists its request state and never initiates a search simply
// because somebody viewed or approved a recommendation.
import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
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

type mediaHuntRequest struct {
	Kind       string    `json:"kind"`
	ExternalID string    `json:"external_id"`
	PlatformID int       `json:"platform_id,omitempty"`
	GameID     int       `json:"game_id,omitempty"`
	Formats    []string  `json:"formats,omitempty"`
	Status     string    `json:"status"`
	Attempts   int       `json:"attempts"`
	LastSearch time.Time `json:"last_search,omitempty"`
	NextSearch time.Time `json:"next_search,omitempty"`
	Error      string    `json:"error,omitempty"`
}
type mediaHuntService struct {
	mu      sync.Mutex
	path    string
	items   []mediaHuntRequest
	running bool
}

func newMediaHuntService() *mediaHuntService {
	p := os.Getenv("MEDIA_HUNT_STATE_FILE")
	if p == "" {
		p = "/config/media-hunt.json"
	}
	s := &mediaHuntService{path: p}
	data, err := os.ReadFile(p)
	if err == nil {
		_ = json.Unmarshal(data, &s.items)
	}
	return s
}
func huntEnvInt(name string, def, min, max int) int {
	v, err := strconv.Atoi(catalogueSetting(name))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
func (s *mediaHuntService) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
func huntKey(a mediaHuntRequest) string {
	return a.Kind + ":" + a.ExternalID + ":" + strconv.Itoa(a.PlatformID) + ":" + strconv.Itoa(a.GameID)
}
func (s *mediaHuntService) enqueue(r mediaHuntRequest) (bool, error) {
	if r.Kind != "book" && r.Kind != "game" {
		return false, errors.New("unsupported kind")
	}
	if len(r.ExternalID) == 0 || len(r.ExternalID) > 100 {
		return false, errors.New("invalid external id")
	}
	if r.Kind == "game" && r.GameID <= 0 {
		return false, errors.New("RetroArr game must already be registered")
	}
	if r.Kind == "book" {
		if len(r.Formats) == 0 {
			return false, errors.New("book requires at least one format")
		}
		for _, f := range r.Formats {
			if f != "eBook" && f != "AudioBook" {
				return false, errors.New("invalid format")
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.items {
		if huntKey(v) == huntKey(r) {
			return false, nil
		}
	}
	r.Status = "pending"
	r.NextSearch = time.Now().UTC()
	s.items = append(s.items, r)
	if err := s.saveLocked(); err != nil {
		s.items = s.items[:len(s.items)-1]
		return false, err
	}
	return true, nil
}
func huntAuth(r *http.Request) bool {
	expected := catalogueSetting("MEDIA_HUNT_TOKEN")
	supplied := r.Header.Get("X-Media-Hunt-Token")
	return expected != "" && len(expected) == len(supplied) && subtle.ConstantTimeCompare([]byte(expected), []byte(supplied)) == 1
}
func (s *mediaHuntService) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/media-hunt", func(w http.ResponseWriter, r *http.Request) {
		if !huntAuth(r) {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		s.mu.Lock()
		data, err := json.Marshal(s.items)
		s.mu.Unlock()
		if err != nil {
			http.Error(w, "serialization failure", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("POST /api/media-hunt", func(w http.ResponseWriter, r *http.Request) {
		if !huntAuth(r) {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		var item mediaHuntRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&item); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		added, err := s.enqueue(item)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"queued": added})
	})
	mux.HandleFunc("POST /api/media-hunt/run", func(w http.ResponseWriter, r *http.Request) {
		if !huntAuth(r) {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		n := s.cycle(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"processed": n})
	})
}
func huntAPI(ctx context.Context, base, apikey, method, path string, params url.Values) error {
	if base == "" || apikey == "" {
		return errors.New("media manager not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return errors.New("invalid media manager URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	if params != nil {
		parsed.RawQuery = params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), nil)
	if err != nil {
		return err
	}
	if strings.Contains(path, "/api/v3/") {
		req.Header.Set("X-Api-Key", apikey)
	}
	client := &http.Client{Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	return nil
}
func dispatchHunt(ctx context.Context, item mediaHuntRequest) error {
	if item.Kind == "game" {
		p := fmt.Sprintf("/api/v3/games/%d/search-now", item.GameID)
		dispatch := "false"
		if catalogueSetting("MEDIA_HUNT_GAME_AUTO_DISPATCH") == "true" {
			dispatch = "true"
		}
		return huntAPI(ctx, catalogueSetting("CATALOGUE_RETROARR_URL"), catalogueSetting("CATALOGUE_RETROARR_KEY"), http.MethodPost, p, url.Values{"autoDispatch": {dispatch}})
	}
	// LazyLibrarian queues both formats independently at approval time. Search
	// requests use its native bulk scheduler, once per cycle rather than per book.
	return nil
}
func (s *mediaHuntService) cycle(ctx context.Context) int {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return 0
	}
	s.running = true
	now := time.Now().UTC()
	limit := huntEnvInt("MEDIA_HUNT_BATCH", 10, 1, 100)
	indexes := make([]int, 0, limit)
	for i, it := range s.items {
		if len(indexes) >= limit {
			break
		}
		if (it.Status == "pending" || it.Status == "retry" || it.Status == "searched") && !now.Before(it.NextSearch) {
			indexes = append(indexes, i)
		}
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
	processed := 0
	books := false
	for _, i := range indexes {
		if ctx.Err() != nil {
			break
		}
		s.mu.Lock()
		item := s.items[i]
		s.mu.Unlock()
		err := dispatchHunt(ctx, item)
		if item.Kind == "book" && err == nil {
			books = true
		}
		s.mu.Lock()
		s.items[i].Attempts++
		s.items[i].LastSearch = now
		if err != nil {
			s.items[i].Status = "retry"
			s.items[i].Error = err.Error()
			s.items[i].NextSearch = now.Add(time.Duration(huntEnvInt("MEDIA_HUNT_RETRY_MINUTES", 360, 15, 10080)) * time.Minute)
		} else {
			s.items[i].Status = "searched"
			s.items[i].Error = ""
			s.items[i].NextSearch = now.Add(time.Duration(huntEnvInt("MEDIA_HUNT_RESCAN_HOURS", 168, 24, 8760)) * time.Hour)
		}
		_ = s.saveLocked()
		s.mu.Unlock()
		processed++
	}
	if books {
		// Global forceBookSearch is intentionally invoked once for the batch, not
		// per title, to avoid multiplying identical searches across thousands of books.
		params := url.Values{"cmd": {"forceBookSearch"}, "apikey": {catalogueSetting("CATALOGUE_LAZYLIBRARIAN_KEY")}}
		var err error
		for _, format := range []string{"eBook", "AudioBook"} {
			params.Set("type", format)
			if e := huntAPI(ctx, catalogueSetting("CATALOGUE_LAZYLIBRARIAN_URL"), catalogueSetting("CATALOGUE_LAZYLIBRARIAN_KEY"), http.MethodGet, "/api", params); e != nil {
				err = e
			}
		}
		if err != nil { // Restore book jobs to retry on a failed global search.
			s.mu.Lock()
			for _, i := range indexes {
				if s.items[i].Kind == "book" && s.items[i].Status == "searched" {
					s.items[i].Status = "retry"
					s.items[i].Error = err.Error()
					s.items[i].NextSearch = now.Add(6 * time.Hour)
				}
			}
			_ = s.saveLocked()
			s.mu.Unlock()
		}
	}
	return processed
}
func (s *mediaHuntService) run(ctx context.Context) {
	// Keep the scheduler alive while disabled so changes made in the web UI
	// take effect without recreating the container.
	ticker := time.NewTicker(time.Minute)
	lastRun := time.Now()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if catalogueSetting("MEDIA_HUNT_ENABLED") == "true" && time.Since(lastRun) >= time.Duration(huntEnvInt("MEDIA_HUNT_INTERVAL_MINUTES", 60, 15, 1440))*time.Minute {
				s.cycle(ctx)
				lastRun = time.Now()
			}
		}
	}
}
