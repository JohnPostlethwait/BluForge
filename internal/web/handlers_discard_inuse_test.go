package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// Both discard endpoints answered every failure with 404. "In use" is not "not
// found": it tells the user to go looking for a copy that is right there and
// busy, when what they need to know is that a rip is reading it and the button
// will work in a few minutes.
func TestDiscardErrorInUseIsAConflict(t *testing.T) {
	err := discardHTTPError(fmt.Errorf("%w: drive 0 has 1 rip(s) in flight", workflow.ErrBackupInUse))

	var he *echo.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("got %T, want *echo.HTTPError", err)
	}
	if he.Code != http.StatusConflict {
		t.Errorf("code = %d, want 409", he.Code)
	}
	if msg := fmt.Sprint(he.Message); !strings.Contains(strings.ToLower(msg), "still reading") {
		t.Errorf("message does not say why it was refused: %q", msg)
	}
}

// A copy that genuinely is not there still has to read as missing.
func TestDiscardErrorMissingCopyIsNotFound(t *testing.T) {
	err := discardHTTPError(errors.New(`no disc copy for "NO_SUCH_DISC"`))

	var he *echo.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("got %T, want *echo.HTTPError", err)
	}
	if he.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", he.Code)
	}
}

// Nothing wrong, nothing to report.
func TestDiscardErrorNilIsNil(t *testing.T) {
	if err := discardHTTPError(nil); err != nil {
		t.Errorf("got %v, want nil", err)
	}
}
