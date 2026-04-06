package github_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	gh "github.com/google/go-github/v72/github"

	"github.com/johnpostlethwait/bluforge/internal/github"
)

func TestNewClientRequiresToken(t *testing.T) {
	_, err := github.NewClient("")
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
}

func TestNewClientAcceptsToken(t *testing.T) {
	c, err := github.NewClient("ghp_testtoken123")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
}

func TestContributionBranchName(t *testing.T) {
	got := github.ContributionBranchName("the-matrix-1999", "2024-blu-ray")
	want := "contribution/the-matrix-1999/2024-blu-ray"
	if got != want {
		t.Errorf("ContributionBranchName: want %q, got %q", want, got)
	}
}

func TestCreateBlob(t *testing.T) {
	imageBytes := []byte("fake image data")
	wantEncoded := base64.StdEncoding.EncodeToString(imageBytes)

	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git/blobs") {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &gotBody)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"sha": "abc123blobsha",
				"url": r.URL.String(),
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ghClient := gh.NewClient(nil).WithAuthToken("test-token")
	srvURL, _ := url.Parse(srv.URL + "/")
	ghClient.BaseURL = srvURL
	c := github.NewClientFromGH(ghClient)

	sha, err := c.CreateBlob(context.Background(), "owner", "repo", imageBytes)
	if err != nil {
		t.Fatalf("CreateBlob: %v", err)
	}
	if sha != "abc123blobsha" {
		t.Errorf("sha: want %q, got %q", "abc123blobsha", sha)
	}
	if gotBody["encoding"] != "base64" {
		t.Errorf("encoding: want %q, got %q", "base64", gotBody["encoding"])
	}
	if gotBody["content"] != wantEncoded {
		t.Errorf("content: want base64 of image bytes, got %q", gotBody["content"])
	}
}
