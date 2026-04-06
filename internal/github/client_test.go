package github_test

import (
	"testing"

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
