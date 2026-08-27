package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

func cachedDiscScan(discName string, sourceFiles ...string) *makemkv.DiscScan {
	s := &makemkv.DiscScan{DiscName: discName, TitleCount: len(sourceFiles)}
	for i, sf := range sourceFiles {
		s.Titles = append(s.Titles, makemkv.TitleInfo{
			Index:      i,
			Attributes: map[int]string{2: "Feature", 9: "1:52:00", 16: sf},
		})
	}
	return s
}

func getScanResult(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/drives/0/scan", nil)
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)
	return rec
}

// The bug, at the endpoint. Pressing Scan with the bonus disc of a two-disc set
// in the drive answered 200 with the main disc's titles, because the cache is
// keyed on the volume label and both discs answer to it. Pressing Scan is a
// request to read the disc, and only reading it can tell the two apart.
func TestPressingScanReadsTheDiscEvenWithACachedScan(t *testing.T) {
	scanner := &blockingScanner{release: make(chan struct{})}
	srv, orch := newScanServer(t, scanner)
	defer close(scanner.release)

	orch.InjectCachedScan(0, cachedDiscScan("SOME_DISC", "00800.mpls"))

	rec := postScan(t, srv)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 — the cached scan was served instead of reading the disc (body: %s)",
			rec.Code, rec.Body.String())
	}
	if !orch.ScanStatus(0).Active {
		t.Error("no scan was started; the request was answered from cache")
	}
}

// Fetching the result of a scan that has finished is a different request from
// asking for one, and it is the one that may use the cache.
func TestScanResultReturnsTheCachedTitles(t *testing.T) {
	srv, orch := newScanServer(t, &blockingScanner{release: make(chan struct{})})
	orch.InjectCachedScan(0, cachedDiscScan("SOME_DISC", "00800.mpls"))

	rec := getScanResult(t, srv)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var got ScanResultJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if len(got.Titles) != 1 {
		t.Errorf("got %d titles, want 1", len(got.Titles))
	}
}

// The page has to be able to say how old a title list is, or one left over from
// the previous disc is indistinguishable from a fresh read of this one.
func TestScanResultSaysWhenTheDiscWasRead(t *testing.T) {
	srv, orch := newScanServer(t, &blockingScanner{release: make(chan struct{})})
	orch.InjectCachedScan(0, cachedDiscScan("SOME_DISC", "00800.mpls"))

	var got ScanResultJSON
	if err := json.Unmarshal(getScanResult(t, srv).Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.CachedAt == 0 {
		t.Error("CachedAt = 0; the page cannot say how old the scan is")
	}
	if got.DiscName != "SOME_DISC" {
		t.Errorf("DiscName = %q, want SOME_DISC", got.DiscName)
	}
}

// Nothing cached is not an error — it is the page asking before any scan has
// run. It must be distinguishable from an empty disc.
func TestScanResultIsEmptyWhenNothingIsCached(t *testing.T) {
	srv, _ := newScanServer(t, &blockingScanner{release: make(chan struct{})})

	if code := getScanResult(t, srv).Code; code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 when no scan has been cached", code)
	}
}

func driveState(t *testing.T, srv *Server) DriveStoreJSON {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/drives/0/state", nil)
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)

	var got DriveStoreJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	return got
}

// A page loading against a cached scan has to be able to say so. Without it the
// user cannot tell a title list left over from the previous disc from one just
// read out of the drive.
func TestDriveStateReportsWhenAScanIsCached(t *testing.T) {
	srv, orch := newScanServer(t, &blockingScanner{release: make(chan struct{})})
	orch.InjectCachedScan(0, cachedDiscScan("SOME_DISC", "00800.mpls"))

	if got := driveState(t, srv).ScanCachedAt; got == 0 {
		t.Error("ScanCachedAt = 0 with a cached scan; the page cannot say the titles are cached")
	}
}

func TestDriveStateReportsNoCacheBeforeAnyScan(t *testing.T) {
	srv, _ := newScanServer(t, &blockingScanner{release: make(chan struct{})})

	if got := driveState(t, srv).ScanCachedAt; got != 0 {
		t.Errorf("ScanCachedAt = %d before any scan, want 0", got)
	}
}
