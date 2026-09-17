package makemkv

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultKeyDBURL is the FindVUK community key database (English), a zip whose
// sole entry is keydb.cfg. MakeMKV reads a KEYDB.cfg placed in its data
// directory (app_DataDir) as a source of Volume Unique Keys, which lets it
// decrypt UHD discs it has no key for yet.
const DefaultKeyDBURL = "http://fvonline-db.bplaced.net/export/keydb_eng.zip"

// keyDBFileName is the name MakeMKV looks for in its data directory.
const keyDBFileName = "KEYDB.cfg"

// maxKeyDBDownload caps the download to guard against a runaway response. The
// real archive is ~25MB; 200MB leaves generous headroom without being unbounded.
const maxKeyDBDownload = 200 << 20

// RefreshResult reports what a keydb refresh produced.
type RefreshResult struct {
	// Bytes is the size of the keydb.cfg written to the data directory.
	Bytes int64
	// Entries is the number of disc key entries in the file (lines beginning
	// with "0x").
	Entries int
}

// refreshOptions holds the injectable knobs a RefreshKeyDB call may override,
// following the package's functional-options pattern so tests need no network.
type refreshOptions struct {
	url    string
	client *http.Client
}

// RefreshOption overrides a RefreshKeyDB default.
type RefreshOption func(*refreshOptions)

// WithKeyDBURL overrides the source URL (used by tests to point at a local
// server instead of FindVUK).
func WithKeyDBURL(url string) RefreshOption {
	return func(o *refreshOptions) { o.url = url }
}

// WithHTTPClient overrides the HTTP client used for the download.
func WithHTTPClient(c *http.Client) RefreshOption {
	return func(o *refreshOptions) { o.client = c }
}

// RefreshKeyDB downloads the FindVUK community key database, extracts its
// keydb.cfg, and writes it to <dataDir>/KEYDB.cfg, replacing any existing file.
//
// The write is atomic: the contents go to a temp file in the same directory and
// are renamed into place, so a failed or partial download never truncates an
// existing KEYDB.cfg.
func RefreshKeyDB(ctx context.Context, dataDir string, opts ...RefreshOption) (RefreshResult, error) {
	o := refreshOptions{url: DefaultKeyDBURL, client: &http.Client{Timeout: 90 * time.Second}}
	for _, opt := range opts {
		opt(&o)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.url, nil)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("keydb: build request: %w", err)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("keydb: download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return RefreshResult{}, fmt.Errorf("keydb: download: unexpected status %s", resp.Status)
	}

	zipped, err := io.ReadAll(io.LimitReader(resp.Body, maxKeyDBDownload))
	if err != nil {
		return RefreshResult{}, fmt.Errorf("keydb: read download: %w", err)
	}

	cfg, err := extractKeyDB(zipped)
	if err != nil {
		return RefreshResult{}, err
	}

	if err := writeFileAtomic(filepath.Join(dataDir, keyDBFileName), cfg); err != nil {
		return RefreshResult{}, err
	}

	return RefreshResult{Bytes: int64(len(cfg)), Entries: countKeyEntries(cfg)}, nil
}

// extractKeyDB returns the bytes of the keydb.cfg entry inside the zip archive.
func extractKeyDB(zipped []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		return nil, fmt.Errorf("keydb: open archive: %w", err)
	}

	for _, f := range zr.File {
		if !strings.EqualFold(filepath.Base(f.Name), "keydb.cfg") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("keydb: open keydb.cfg in archive: %w", err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return nil, fmt.Errorf("keydb: read keydb.cfg in archive: %w", err)
		}
		return data, nil
	}

	return nil, fmt.Errorf("keydb: archive contains no keydb.cfg")
}

// countKeyEntries counts disc key entries — lines that begin with "0x" — in a
// keydb.cfg. Comments and blank lines are ignored.
func countKeyEntries(cfg []byte) int {
	n := 0
	for _, line := range strings.Split(string(cfg), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "0x") {
			n++
		}
	}
	return n
}

// writeFileAtomic writes data to path via a temp file in the same directory
// followed by a rename, so readers never see a partial file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".keydb-*.tmp")
	if err != nil {
		return fmt.Errorf("keydb: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("keydb: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("keydb: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("keydb: install keydb: %w", err)
	}
	return nil
}
