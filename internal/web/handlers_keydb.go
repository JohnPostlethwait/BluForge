package web

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// defaultMakeMKVDataDir is where MakeMKV's persisted data (app_DataDir) lives in
// the container — the same path main.go configures. KEYDB.cfg is written here.
const defaultMakeMKVDataDir = "/config/.MakeMKV"

// handleRefreshKeyDB downloads the FindVUK community key database and installs
// it as KEYDB.cfg in MakeMKV's data directory, then redirects back to the
// settings page with the outcome. MakeMKV reads that file on its next launch,
// so no restart is needed.
func (s *Server) handleRefreshKeyDB(c echo.Context) error {
	dataDir := s.keydbDataDir
	if dataDir == "" {
		dataDir = defaultMakeMKVDataDir
	}

	var opts []makemkv.RefreshOption
	if s.keydbURL != "" {
		opts = append(opts, makemkv.WithKeyDBURL(s.keydbURL))
	}

	res, err := makemkv.RefreshKeyDB(c.Request().Context(), dataDir, opts...)
	if err != nil {
		slog.Error("keydb refresh failed", "error", err)
		return c.Redirect(http.StatusSeeOther, "/settings?keydb=error")
	}

	slog.Info("keydb refreshed", "entries", res.Entries, "bytes", res.Bytes)
	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/settings?keydb=ok&keys=%d", res.Entries))
}
