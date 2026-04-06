package github_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gh "github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"

	ghpkg "github.com/johnpostlethwait/bluforge/internal/github"
)

// newTestClient wires a Client against a test HTTP server.
func newTestClient(t *testing.T, srv *httptest.Server) *ghpkg.Client {
	t.Helper()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})
	tc := oauth2.NewClient(context.Background(), ts)
	ghClient := gh.NewClient(tc)
	ghClient.BaseURL, _ = ghClient.BaseURL.Parse(srv.URL + "/")
	ghClient.UploadURL, _ = ghClient.UploadURL.Parse(srv.URL + "/")
	return ghpkg.NewClientFromGH(ghClient)
}

func TestWaitForRepo_SucceedsImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":1,"name":"data","full_name":"testuser/data"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	ctx := context.Background()
	if err := c.WaitForRepo(ctx, "testuser", "data"); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestWaitForRepo_TimesOutWhenRepoNeverAppears(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	// Use a context that cancels quickly so the test doesn't take 30 seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := c.WaitForRepo(ctx, "testuser", "data")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
