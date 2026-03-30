package web

import "github.com/labstack/echo/v4"

// csrfToken extracts the CSRF token set by the CSRF middleware. Returns an
// empty string if the middleware did not run (e.g. for skipped routes).
func csrfToken(c echo.Context) string {
	tok := c.Get("csrf")
	if tok == nil {
		return ""
	}
	s, _ := tok.(string)
	return s
}
