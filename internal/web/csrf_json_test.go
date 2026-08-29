package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
)

// csrfServer builds a server through NewServer so the real middleware stack —
// including the CSRF middleware — is in play. Nothing else in the web tests
// does this; they hang handlers off a bare echo instance, which is why the
// skipper's effect was never exercised by anything.
func csrfServer(t *testing.T) (*Server, *db.Store) {
	t.Helper()

	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := config.AppConfig{OutputDir: t.TempDir(), Port: 9160}
	srv := NewServer(ServerDeps{
		Config:   &cfg,
		Store:    store,
		DriveMgr: drivemanager.NewManager(&stubExecutor{}, nil),
		SSEHub:   NewSSEHub(),
	})
	return srv, store
}

var csrfTokenRe = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

// csrfCredentials performs the GET a browser would do first, returning the
// cookie it was issued and the token minted alongside it.
func csrfCredentials(t *testing.T, srv *Server) (cookie, token string) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings", nil))

	for _, c := range rec.Result().Cookies() {
		if strings.EqualFold(c.Name, "_csrf") {
			cookie = c.Name + "=" + c.Value
		}
	}
	if m := csrfTokenRe.FindStringSubmatch(rec.Body.String()); len(m) == 2 {
		token = m[1]
	}
	if cookie == "" || token == "" {
		t.Fatalf("could not obtain CSRF credentials (cookie=%q token=%q)", cookie, token)
	}
	return cookie, token
}

// rejectedByCSRF reports whether the middleware turned the request away.
// Echo answers 400 for a missing token and 403 for an invalid one; either
// means the handler did not run, which is the property that matters.
func rejectedByCSRF(code int) bool {
	return code == http.StatusBadRequest || code == http.StatusForbidden
}

// The CSRF middleware skipped every non-GET request whose Accept header was
// exactly "application/json", reasoning that such requests come from this app's
// own Alpine fetch() calls and that the same-origin policy stops anyone else
// making them.
//
// It does not. Accept is a CORS-safelisted request header, so a cross-origin
// fetch carrying it triggers no preflight: the browser sends the request with
// the user's cookies attached and merely hides the response. By then the side
// effect has happened. BluForge has no authentication of any kind, so this was
// the only thing standing between any page the user visits and their ripper.
func TestJSONPostWithoutATokenIsRejected(t *testing.T) {
	srv, _ := csrfServer(t)

	for _, path := range []string{
		"/drives/0/clear-match",
		"/drives/0/scan",
		"/activity/clear-history",
		"/drives/0/discard-backup",
		"/drives/0/salvage",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			req.Header.Set("Accept", "application/json")
			rec := httptest.NewRecorder()
			srv.echo.ServeHTTP(rec, req)

			if !rejectedByCSRF(rec.Code) {
				t.Errorf("status = %d — the handler ran for a request any cross-origin page can send",
					rec.Code)
			}
		})
	}
}

// The status codes above prove the middleware answered. This proves the
// handler did not: clear-history deletes every rip in the database, and a
// forged request must leave them alone.
func TestAForgedClearHistoryDeletesNothing(t *testing.T) {
	srv, store := csrfServer(t)

	if _, err := store.CreateJob(db.RipJob{
		DriveIndex: 0, DiscName: "KEEP_ME", TitleName: "Feature", Status: "completed",
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/activity/clear-history", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	jobs, err := store.ListAllJobs(10, 0)
	if err != nil {
		t.Fatalf("ListAllJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("%d jobs left after a forged clear-history, want 1 — the history was wiped by a request "+
			"any website could have made (status was %d)", len(jobs), rec.Code)
	}
}

// The app's own requests must keep working, which means the token has to be
// accepted from the header the middleware is configured to read.
func TestJSONPostWithAValidTokenIsAccepted(t *testing.T) {
	srv, _ := csrfServer(t)
	cookie, token := csrfCredentials(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/activity/clear-history", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Cookie", cookie)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rejectedByCSRF(rec.Code) {
		t.Fatalf("a request carrying a valid token was turned away: %d %s", rec.Code, rec.Body.String())
	}
}

// Form posts already carried _csrf and must be unaffected.
func TestFormPostWithoutATokenIsStillRejected(t *testing.T) {
	srv, _ := csrfServer(t)

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("output_dir=/tmp"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if !rejectedByCSRF(rec.Code) {
		t.Errorf("status = %d, want the request turned away", rec.Code)
	}
}
