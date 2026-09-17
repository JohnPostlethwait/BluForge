package makemkv

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// zipWith builds an in-memory zip archive containing a single file with the
// given name and contents — a stand-in for FindVUK's keydb_eng.zip.
func zipWith(t *testing.T, name, contents string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write([]byte(contents)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

const sampleKeyDB = `# FindVUK keydb sample
0xAAAABBBBCCCCDDDDEEEEFFFF0000111122223333 = MOVIE_ONE | V | 0x11111111111111111111111111111111
0x4444555566667777888899990000AAAABBBBCCCC = MOVIE_TWO | V | 0x22222222222222222222222222222222
`

func TestRefreshKeyDB_WritesFileAndCountsEntries(t *testing.T) {
	body := zipWith(t, "keydb.cfg", sampleKeyDB)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dataDir := t.TempDir()

	res, err := RefreshKeyDB(context.Background(), dataDir, WithKeyDBURL(srv.URL))
	if err != nil {
		t.Fatalf("RefreshKeyDB: %v", err)
	}

	// The keydb.cfg entry from the zip should land at <dataDir>/KEYDB.cfg verbatim.
	got, err := os.ReadFile(filepath.Join(dataDir, "KEYDB.cfg"))
	if err != nil {
		t.Fatalf("read KEYDB.cfg: %v", err)
	}
	if string(got) != sampleKeyDB {
		t.Errorf("KEYDB.cfg contents mismatch:\n got: %q\nwant: %q", got, sampleKeyDB)
	}

	if res.Entries != 2 {
		t.Errorf("Entries = %d, want 2", res.Entries)
	}
	if res.Bytes != int64(len(sampleKeyDB)) {
		t.Errorf("Bytes = %d, want %d", res.Bytes, len(sampleKeyDB))
	}
}

func TestRefreshKeyDB_FailedDownloadKeepsExistingFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	existing := "0xDEADBEEF = OLD_KEYS\n"
	dest := filepath.Join(dataDir, "KEYDB.cfg")
	if err := os.WriteFile(dest, []byte(existing), 0o600); err != nil {
		t.Fatalf("seed KEYDB.cfg: %v", err)
	}

	if _, err := RefreshKeyDB(context.Background(), dataDir, WithKeyDBURL(srv.URL)); err == nil {
		t.Fatal("expected error on HTTP 500, got nil")
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read KEYDB.cfg: %v", err)
	}
	if string(got) != existing {
		t.Errorf("existing KEYDB.cfg was modified on failure: got %q, want %q", got, existing)
	}
}

func TestRefreshKeyDB_ArchiveWithoutKeyDBErrors(t *testing.T) {
	body := zipWith(t, "readme.txt", "not a keydb")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	if _, err := RefreshKeyDB(context.Background(), t.TempDir(), WithKeyDBURL(srv.URL)); err == nil {
		t.Fatal("expected error when archive has no keydb.cfg, got nil")
	}
}
