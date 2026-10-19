package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMediaHuntQueueAndPersistence(t *testing.T) {
	s := &mediaHuntService{path: filepath.Join(t.TempDir(), "state.json")}
	a := mediaHuntRequest{Kind: "game", ExternalID: "123", GameID: 12, PlatformID: 3}
	ok, err := s.enqueue(a)
	if err != nil || !ok {
		t.Fatalf("enqueue %v %v", ok, err)
	}
	ok, err = s.enqueue(a)
	if err != nil || ok {
		t.Fatalf("duplicate %v %v", ok, err)
	}
	s2 := &mediaHuntService{path: s.path}
	if err := readStateForTest(s2); err != nil {
		t.Fatal(err)
	}
	if len(s2.items) != 1 || s2.items[0].GameID != 12 {
		t.Fatal("persistence failure")
	}
}
func readStateForTest(s *mediaHuntService) error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.items)
}
func TestMediaHuntRequiresToken(t *testing.T) {
	s := &mediaHuntService{path: filepath.Join(t.TempDir(), "state.json")}
	mux := http.NewServeMux()
	s.register(mux)
	r := httptest.NewRequest("POST", "/api/media-hunt", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
func TestMediaHuntDispatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v3/games/17/search-now" || r.URL.Query().Get("autoDispatch") != "false" || r.Header.Get("X-Api-Key") != "test-key" {
			t.Errorf("invalid request: %v", r.URL)
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	t.Setenv("CATALOGUE_RETROARR_URL", upstream.URL)
	t.Setenv("CATALOGUE_RETROARR_KEY", "test-key")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	if err := dispatchHunt(ctx, mediaHuntRequest{Kind: "game", GameID: 17}); err != nil {
		t.Fatal(err)
	}
}
