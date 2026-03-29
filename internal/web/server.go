package web

import (
	"context"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// ServerDeps groups all dependencies required by the Server.
type ServerDeps struct {
	Config       *config.AppConfig
	Store        *db.Store
	DriveMgr     *drivemanager.Manager
	RipEngine    *ripper.Engine
	DiscDBClient *discdb.Client
	SSEHub       *SSEHub
}

// Server wraps an Echo instance and all application dependencies.
type Server struct {
	echo         *echo.Echo
	cfg          *config.AppConfig
	store        *db.Store
	driveMgr     *drivemanager.Manager
	ripEngine    *ripper.Engine
	discdbClient *discdb.Client
	sseHub       *SSEHub
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
		store:        deps.Store,
		driveMgr:     deps.DriveMgr,
		ripEngine:    deps.RipEngine,
		discdbClient: deps.DiscDBClient,
		sseHub:       deps.SSEHub,
	}

	// Static files
	e.Static("/static", "static")

	// Routes
	e.GET("/", s.handleDashboard)
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
	e.POST("/drives/:id/contribute", s.handleContributeSubmit)

	return s
}

// Start begins listening on the configured port.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.Port)
	return s.echo.Start(addr)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	return s.echo.Shutdown(context.Background())
}

// --- Placeholder handlers ---

func (s *Server) handleDashboard(c echo.Context) error {
	return c.String(http.StatusOK, "coming soon")
}

func (s *Server) handleDriveDetail(c echo.Context) error {
	return c.String(http.StatusOK, "coming soon")
}

func (s *Server) handleDriveSearch(c echo.Context) error {
	return c.String(http.StatusOK, "coming soon")
}

func (s *Server) handleDriveRip(c echo.Context) error {
	return c.String(http.StatusOK, "coming soon")
}

func (s *Server) handleDriveRescan(c echo.Context) error {
	return c.String(http.StatusOK, "coming soon")
}

func (s *Server) handleQueue(c echo.Context) error {
	return c.String(http.StatusOK, "coming soon")
}

func (s *Server) handleHistory(c echo.Context) error {
	return c.String(http.StatusOK, "coming soon")
}

func (s *Server) handleSettings(c echo.Context) error {
	return c.String(http.StatusOK, "coming soon")
}

func (s *Server) handleSettingsSave(c echo.Context) error {
	return c.String(http.StatusOK, "coming soon")
}

func (s *Server) handleContribute(c echo.Context) error {
	return c.String(http.StatusOK, "coming soon")
}

func (s *Server) handleContributeSubmit(c echo.Context) error {
	return c.String(http.StatusOK, "coming soon")
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
