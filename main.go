package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/internal/web"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// sseAdapter wraps web.SSEHub so it satisfies workflow.Broadcaster without
// creating an import cycle between the web and workflow packages.
type sseAdapter struct {
	hub *web.SSEHub
}

func (a *sseAdapter) Broadcast(msg workflow.SSEMessage) {
	a.hub.Broadcast(web.SSEEvent{Event: msg.Event, Data: msg.Data})
}

func main() {
	// 1. Structured JSON logging to stdout.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// 2. Load config.
	cfg, err := config.Load("/config/config.yaml")
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// 3. Open SQLite database.
	store, err := db.Open("/config/bluforge.db")
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	// 4. Create MakeMKV executor.
	executor := makemkv.NewExecutor()

	// 5. Create TheDiscDB client.
	discdbClient := discdb.NewClient()

	// 6. Create TheDiscDB cache.
	discdbCache := discdb.NewCache(store, 24*time.Hour)

	// 7. Create SSE hub.
	sseHub := web.NewSSEHub()

	// 8. Create organizer.
	org := organizer.New(cfg.MovieTemplate, cfg.SeriesTemplate)

	// 9. Create rip engine with onUpdate callback.
	ripEngine := ripper.NewEngine(executor)
	ripEngine.OnUpdate(func(job *ripper.Job) {
		slog.Info("rip job update", "drive_index", job.DriveIndex, "status", job.Status, "progress", job.Progress)
		data, err := json.Marshal(job)
		if err != nil {
			slog.Error("failed to marshal rip job", "error", err)
			return
		}
		sseHub.Broadcast(web.SSEEvent{Event: "rip-update", Data: string(data)})
	})

	// 10. Create workflow orchestrator.
	orch := workflow.NewOrchestrator(workflow.OrchestratorDeps{
		Store:     store,
		Engine:    ripEngine,
		Organizer: org,
		SSEHub:    &sseAdapter{hub: sseHub},
		Scanner:   executor,
		DiscDB:    discdbClient,
		Cache:     discdbCache,
	})

	// 11. Create drive manager with onEvent callback.
	driveMgr := drivemanager.NewManager(executor, func(ev drivemanager.DriveEvent) {
		slog.Info("drive event", "type", ev.Type, "drive_index", ev.DriveIndex, "disc_name", ev.DiscName)
		data, err := json.Marshal(ev)
		if err != nil {
			slog.Error("failed to marshal drive event", "error", err)
			return
		}
		sseHub.Broadcast(web.SSEEvent{Event: "drive-event", Data: string(data)})

		// Auto-rip: trigger on disc insert when enabled.
		if ev.Type == drivemanager.EventDiscInserted && cfg.AutoRip {
			go func() {
				slog.Info("auto-rip triggered", "drive_index", ev.DriveIndex, "disc_name", ev.DiscName)
				autoErr := orch.AutoRip(context.Background(), ev.DriveIndex, workflow.AutoRipConfig{
					OutputDir:       cfg.OutputDir,
					MovieTemplate:   cfg.MovieTemplate,
					SeriesTemplate:  cfg.SeriesTemplate,
					DuplicateAction: cfg.DuplicateAction,
				})
				if autoErr != nil {
					slog.Error("auto-rip failed", "error", autoErr, "drive_index", ev.DriveIndex)
				}
			}()
		}
	})

	// 12. Create web server with all dependencies.
	srv := web.NewServer(web.ServerDeps{
		Config:       &cfg,
		Store:        store,
		DriveMgr:     driveMgr,
		RipEngine:    ripEngine,
		DiscDBClient: discdbClient,
		DiscDBCache:  discdbCache,
		SSEHub:       sseHub,
		Orchestrator: orch,
	})

	// 13. Set up graceful shutdown with signal.NotifyContext.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 14. Start drive manager polling in a goroutine.
	pollInterval := time.Duration(cfg.PollInterval) * time.Second
	go driveMgr.Run(ctx, pollInterval)

	// 15. Start web server in a goroutine.
	go func() {
		if err := srv.Start(); err != nil {
			slog.Info("web server stopped", "error", err)
		}
	}()

	// 16. Log "BluForge ready" with URL.
	slog.Info("BluForge ready", "url", fmt.Sprintf("http://0.0.0.0:%d", cfg.Port))

	// 17. Wait for shutdown signal, then stop server.
	<-ctx.Done()
	slog.Info("shutdown signal received, stopping server")
	if err := srv.Stop(); err != nil {
		slog.Error("error stopping server", "error", err)
	}
	slog.Info("BluForge stopped")
}
