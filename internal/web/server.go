package web

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

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
}

// Server wraps an Echo instance and all application dependencies.
type Server struct {
	echo         *echo.Echo
	cfgMu        sync.RWMutex
	cfg          *config.AppConfig
	configPath   string
	store        *db.Store
	driveMgr     *drivemanager.Manager
	ripEngine    *ripper.Engine
	discdbClient *discdb.Client
	discdbCache  *discdb.Cache
	sseHub        *SSEHub
	orchestrator  *workflow.Orchestrator
	driveSessions *DriveSessionStore
}

// NewServer creates and configures a new Server from the provided dependencies.
func NewServer(deps ServerDeps) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.Use(middleware.Recover())
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogMethod: true,
		LogURI:    true,
		LogStatus: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			return nil
		},
	}))

	s := &Server{
		echo:         e,
		cfg:          deps.Config,
		configPath:   deps.ConfigPath,
		store:        deps.Store,
		driveMgr:     deps.DriveMgr,
		ripEngine:    deps.RipEngine,
		discdbClient: deps.DiscDBClient,
		discdbCache:  deps.DiscDBCache,
		sseHub:        deps.SSEHub,
		orchestrator:  deps.Orchestrator,
		driveSessions: NewDriveSessionStore(),
	}

	// Static files
	e.Static("/static", "static")

	// Routes
	e.GET("/", s.handleDashboard)
	e.GET("/drives-partial", s.handleDrivesPartial)
	e.GET("/drives/:id", s.handleDriveDetail)
	e.POST("/drives/:id/search", s.handleDriveSearch)
	e.POST("/drives/:id/rip", s.handleDriveRip)
	e.POST("/drives/:id/rescan", s.handleDriveRescan)
	e.GET("/queue", s.handleQueue)
	e.GET("/history", s.handleHistory)
	e.GET("/settings", s.handleSettings)
	e.POST("/settings", s.handleSettingsSave)
	e.GET("/events", s.handleSSE)
	e.GET("/drives/:id/contribute", s.handleContribute)

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

// handleSSE streams Server-Sent Events to the connected client.
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
		}
	}
}
