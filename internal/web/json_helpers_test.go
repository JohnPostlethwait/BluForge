package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestWantsJSON_ApplicationJSON(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if !wantsJSON(c) {
		t.Error("expected wantsJSON to return true for Accept: application/json")
	}
}

func TestWantsJSON_TextHTML(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if wantsJSON(c) {
		t.Error("expected wantsJSON to return false for Accept: text/html")
	}
}

func TestWantsJSON_NoHeader(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if wantsJSON(c) {
		t.Error("expected wantsJSON to return false when no Accept header")
	}
}
