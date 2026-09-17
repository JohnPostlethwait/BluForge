package web

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func keydbZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("keydb.cfg")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	_, _ = w.Write([]byte("0xAAAA = MOVIE_ONE | V | 0x1111\n"))
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestHandleRefreshKeyDB_DownloadsAndRedirects(t *testing.T) {
	body := keydbZip(t)
	fv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer fv.Close()

	dataDir := t.TempDir()

	e := echo.New()
	s := &Server{
		echo:         e,
		keydbURL:     fv.URL,
		keydbDataDir: dataDir,
	}
	e.POST("/settings/refresh-keydb", s.handleRefreshKeyDB)

	req := httptest.NewRequest(http.MethodPost, "/settings/refresh-keydb", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "keydb=ok") {
		t.Errorf("Location = %q, want it to contain keydb=ok", loc)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "KEYDB.cfg")); err != nil {
		t.Errorf("KEYDB.cfg not written: %v", err)
	}
}

func TestHandleRefreshKeyDB_FailureRedirectsWithError(t *testing.T) {
	fv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer fv.Close()

	e := echo.New()
	s := &Server{
		echo:         e,
		keydbURL:     fv.URL,
		keydbDataDir: t.TempDir(),
	}
	e.POST("/settings/refresh-keydb", s.handleRefreshKeyDB)

	req := httptest.NewRequest(http.MethodPost, "/settings/refresh-keydb", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "keydb=error") {
		t.Errorf("Location = %q, want it to contain keydb=error", loc)
	}
}
