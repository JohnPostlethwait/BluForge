package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

func TestHandleContributionResetPR_NotSubmitted(t *testing.T) {
	s, store := setupContribServer(t)

	id, err := store.SaveContribution(db.Contribution{
		DiscKey:          "RESET-PENDING",
		DiscName:         "Pending Disc",
		ContributionType: "add",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/contributions/%d/reset-pr", id), nil)
	rec := httptest.NewRecorder()
	c := s.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	err = s.handleContributionResetPR(c)
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("expected *echo.HTTPError, got %T: %v", err, err)
	}
	if he.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", he.Code, http.StatusBadRequest)
	}
}

func TestHandleContributionResetPR_Submitted(t *testing.T) {
	s, store := setupContribServer(t)

	id, err := store.SaveContribution(db.Contribution{
		DiscKey:          "RESET-SUBMITTED",
		DiscName:         "Submitted Disc",
		ContributionType: "add",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.UpdateContributionStatus(id, "submitted", "https://github.com/TheDiscDb/data/pull/1"); err != nil {
		t.Fatalf("set submitted: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/contributions/%d/reset-pr", id), nil)
	rec := httptest.NewRecorder()
	c := s.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	err = s.handleContributionResetPR(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	got, err := store.GetContribution(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("status = %q, want %q", got.Status, "pending")
	}
	if got.PRURL != "" {
		t.Errorf("pr_url = %q, want empty", got.PRURL)
	}
}
