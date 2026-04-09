package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestParseDriveIndex_Valid(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"0", 0},
		{"1", 1},
		{"99", 99},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(tc.input)

			got, err := parseDriveIndex(c)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseDriveIndex(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseDriveIndex_Invalid(t *testing.T) {
	tests := []string{"abc", "-1x", "", "3.5"}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(input)

			_, err := parseDriveIndex(c)
			if err == nil {
				t.Errorf("parseDriveIndex(%q): expected error, got nil", input)
			}
		})
	}
}

func TestNormalizeSearchQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"underscores to spaces", "THE_BIG_LEBOWSKI", "THE BIG LEBOWSKI"},
		{"hyphens to spaces", "the-big-lebowski", "the big lebowski"},
		{"mixed separators", "THE_BIG-LEBOWSKI", "THE BIG LEBOWSKI"},
		{"collapses whitespace", "  too   many  spaces  ", "too many spaces"},
		{"combined", "THE_BIG__LEBOWSKI--DISC_1", "THE BIG LEBOWSKI DISC 1"},
		{"empty string", "", ""},
		{"already clean", "Seinfeld", "Seinfeld"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeSearchQuery(tc.input)
			if got != tc.want {
				t.Errorf("normalizeSearchQuery(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateLangCodes_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"single code", "eng"},
		{"multiple codes", "eng,fra,deu"},
		{"codes with spaces", "eng, fra, deu"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateLangCodes(tc.input, "audio language"); err != nil {
				t.Errorf("validateLangCodes(%q) returned unexpected error: %v", tc.input, err)
			}
		})
	}
}

func TestValidateLangCodes_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"full word", "english"},
		{"two letters", "en"},
		{"uppercase", "ENG"},
		{"numbers", "123"},
		{"mixed valid and invalid", "eng,french"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateLangCodes(tc.input, "audio language"); err == nil {
				t.Errorf("validateLangCodes(%q): expected error, got nil", tc.input)
			}
		})
	}
}

func TestValidateLangCodes_Empty(t *testing.T) {
	if err := validateLangCodes("", "audio language"); err != nil {
		t.Errorf("validateLangCodes(\"\") returned unexpected error: %v", err)
	}
}
