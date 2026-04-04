package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// ServerDeps groups all dependencies required by the Server.
type ServerDeps struct {
	Config       *config.AppConfig
	ConfigPath   string
	Store        *db.Store
	DriveMgr     *drivemanager.Manager
	RipEngine    *ripper.Engine
	DiscDBClient *discdb.Client
	DiscDBCache  *discdb.Cache
	SSEHub       *SSEHub
	Orchestrator *workflow.Orchestrator
	// OnMakeMKVKeyChange is called after the MakeMKV registration key is updated
	// via the Settings UI. It receives the new key (empty string = clear key).
	OnMakeMKVKeyChange func(string)
}

// Server wraps an Echo instance and all application dependencies.
type Server struct {
	echo               *echo.Echo
	cfgMu              sync.RWMutex
	cfg                *config.AppConfig
	configPath         string
	store              *db.Store
	driveMgr           *drivemanager.Manager
	ripEngine          *ripper.Engine
	discdbClient       *discdb.Client
	discdbCache        *discdb.Cache
	sseHub             *SSEHub
	orchestrator       *workflow.Orchestrator
	driveSessions      *DriveSessionStore
	onMakeMKVKeyChange func(string)
}

// NewServer creates and configures a new Server from the provided dependencies.
func NewServer(deps ServerDeps) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// Custom error handler: log full details but return generic messages to clients.
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		if c.Response().Committed {
			return
		}
		he, ok := err.(*echo.HTTPError)
		if ok {
			if he.Internal != nil {
				slog.Error("http error", "status", he.Code, "internal", he.Internal)
			}
			_ = c.String(he.Code, fmt.Sprintf("%v", he.Message))
			return
		}
		slog.Error("unhandled error", "error", err, "uri", c.Request().RequestURI)
		_ = c.String(http.StatusInternalServerError, "An internal error occurred.")
	}

	e.Use(middleware.Recover())

	// Security headers.
	e.Use(middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection:      "1; mode=block",
		ContentTypeNosniff: "nosniff",
		XFrameOptions:      "DENY",
		ReferrerPolicy:     "strict-origin-when-cross-origin",
		ContentSecurityPolicy: "default-src 'self'; " +
			"script-src 'self' 'unsafe-inline' 'unsafe-eval' https://unpkg.com https://cdn.jsdelivr.net; " +
			"style-src 'self' 'unsafe-inline'; " +
			"connect-src 'self'; " +
			"img-src 'self' data:;",
	}))

	// CSRF protection for state-changing endpoints. The middleware must run on
	// GET requests to set the cookie and generate the token (it only validates
	// on POST/PUT/DELETE/PATCH). Skip only for JSON API POSTs (Alpine fetch).
	e.Use(middleware.CSRFWithConfig(middleware.CSRFConfig{
		TokenLookup:    "form:_csrf,header:X-CSRF-Token",
		CookiePath:     "/",
		CookieHTTPOnly: true,
		CookieSameSite: http.SameSiteStrictMode,
		Skipper: func(c echo.Context) bool {
			// JSON API requests (Alpine fetch) handle auth differently.
			if c.Request().Method != http.MethodGet {
				accept := c.Request().Header.Get("Accept")
				return accept == "application/json"
			}
			return false
		},
	}))

	// Rate limiter: 20 requests/second with a burst of 40.
	e.Use(middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:  rate.Limit(20),
				Burst: 40,
			},
		),
	}))

	s := &Server{
		echo:               e,
		cfg:                deps.Config,
		configPath:         deps.ConfigPath,
		store:              deps.Store,
		driveMgr:           deps.DriveMgr,
		ripEngine:          deps.RipEngine,
		discdbClient:       deps.DiscDBClient,
		discdbCache:        deps.DiscDBCache,
		sseHub:             deps.SSEHub,
		orchestrator:       deps.Orchestrator,
		driveSessions:      NewDriveSessionStore(),
		onMakeMKVKeyChange: deps.OnMakeMKVKeyChange,
	}

	// Static files
	e.Static("/static", "static")

	// Routes
	e.GET("/", s.handleDashboard)
	e.GET("/drives/:id", s.handleDriveDetail)
	e.POST("/drives/:id/search", s.handleDriveSearch)
	e.POST("/drives/:id/select", s.handleDriveSelectAlpine)
	e.POST("/drives/:id/scan", s.handleDriveScan)
	e.POST("/drives/:id/rip", s.handleDriveRip)
	e.POST("/drives/:id/rescan", s.handleDriveRescan)
	e.POST("/drives/:id/match", s.handleDriveMatch)
	e.GET("/activity", s.handleActivity)
	e.POST("/activity/:id/cancel", s.handleActivityCancel)
	e.POST("/activity/clear-history", s.handleActivityClearHistory)
	e.POST("/activity/clear-filtered", s.handleActivityClearFiltered)
	e.GET("/settings", s.handleSettings)
	e.POST("/settings", s.handleSettingsSave)
	e.GET("/contributions", s.handleContributions)
	e.GET("/contributions/:id", s.handleContributionDetail)
	e.POST("/contributions/:id", s.handleContributionSave)
	e.POST("/contributions/:id/submit", s.handleContributionSubmit)
	e.GET("/events", s.handleSSE)

	return s
}

// GetConfig returns a snapshot of the current configuration. Safe for
// concurrent use; callers receive a copy so mutations do not affect shared
// state.
func (s *Server) GetConfig() config.AppConfig {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return *s.cfg
}

// UpdateConfig applies fn to a locked copy of the config, writes it to disk,
// and replaces the in-memory value. The mutex is held for the duration so
// that concurrent readers always see a consistent value.
func (s *Server) UpdateConfig(fn func(*config.AppConfig)) error {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	fn(s.cfg)

	return config.Save(*s.cfg, s.configPath)
}

// Start begins listening on the configured port.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.GetConfig().Port)
	return s.echo.Start(addr)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	return s.echo.Shutdown(context.Background())
}

// ClearDriveSession removes the drive session for the given index.
// Called when a disc is ejected to clear stale selection state.
func (s *Server) ClearDriveSession(driveIndex int) {
	s.driveSessions.Clear(driveIndex)
}

// handleSSE streams Server-Sent Events to the connected client.
// Clients that have received no events for 5 minutes receive a keepalive
// comment to detect dead connections. If even the keepalive write fails the
// connection is closed.
func (s *Server) handleSSE(c echo.Context) error {
	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch := s.sseHub.Subscribe()
	defer s.sseHub.Unsubscribe(ch)

	ctx := c.Request().Context()
	keepalive := time.NewTicker(5 * time.Minute)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, ev.Data)
			w.Flush()
			keepalive.Reset(5 * time.Minute)
		case <-keepalive.C:
			// Keepalive comment — detects dead connections.
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return nil
			}
			w.Flush()
		}
	}
}
