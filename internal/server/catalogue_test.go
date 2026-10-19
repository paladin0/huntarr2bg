package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogueRequiresConfiguration(t *testing.T) {
	t.Setenv("CATALOGUE_LAZYLIBRARIAN_URL", "")
	t.Setenv("CATALOGUE_LAZYLIBRARIAN_KEY", "")
	w := httptest.NewRecorder()
	catalogueProxy(w, httptest.NewRequest("GET", "/api/catalogue/authors", nil), "authors")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", w.Code)
	}
}

func TestCatalogueAuthorsProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" || r.URL.Query().Get("cmd") != "getIndex" || r.URL.Query().Get("apikey") != "secret" {
			t.Errorf("wrong upstream path or arguments")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"AuthorName":"Test Author"}]`))
	}))
	defer upstream.Close()
	t.Setenv("CATALOGUE_LAZYLIBRARIAN_URL", upstream.URL)
	t.Setenv("CATALOGUE_LAZYLIBRARIAN_KEY", "secret")
	w := httptest.NewRecorder()
	catalogueProxy(w, httptest.NewRequest("GET", "/api/catalogue/authors", nil), "authors")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Test Author") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCatalogueInvalidURL(t *testing.T) {
	t.Setenv("CATALOGUE_RETROARR_URL", "file:///etc/passwd")
	t.Setenv("CATALOGUE_RETROARR_KEY", "secret")
	w := httptest.NewRecorder()
	catalogueProxy(w, httptest.NewRequest("GET", "/api/catalogue/games", nil), "games")
	if w.Code != 400 {
		t.Fatalf("got %d", w.Code)
	}
}
