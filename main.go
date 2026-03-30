package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
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
	slog.Info("config loaded",
		"output_dir", cfg.OutputDir,
		"auto_rip", cfg.AutoRip,
		"poll_interval", cfg.PollInterval,
		"min_title_length", cfg.MinTitleLength,
		"duplicate_action", cfg.DuplicateAction,
	)

	// 3. Open SQLite database.
	store, err := db.Open("/config/bluforge.db")
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	slog.Info("database opened", "path", "/config/bluforge.db")

	// 4. Create MakeMKV executor and verify it works.
	executor := makemkv.NewExecutor()
	if path, err := exec.LookPath("makemkvcon"); err != nil {
		slog.Error("makemkvcon not found in PATH", "error", err)
	} else {
		slog.Info("makemkvcon found", "path", path)
	}

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
		OnBroadcast: func(event, data string) {
			sseHub.Broadcast(web.SSEEvent{Event: event, Data: data})
		},
		Scanner: executor,
		DiscDB:  discdbClient,
		Cache:   discdbCache,
	})

	// 11. Create drive manager with onEvent callback.
	// srv and driveMgr are declared here so the callback closure can read live
	// config via srv.GetConfig() and reference driveMgr.GetAllDrives() without
	// a forward-reference error.
	var srv *web.Server
	var driveMgr *drivemanager.Manager
	driveMgr = drivemanager.NewManager(executor, func(ev drivemanager.DriveEvent) {
		slog.Info("drive event", "type", ev.Type, "drive_index", ev.DriveIndex, "disc_name", ev.DiscName)
		data, err := json.Marshal(ev)
		if err != nil {
			slog.Error("failed to marshal drive event", "error", err)
			return
		}
		sseHub.Broadcast(web.SSEEvent{Event: "drive-event", Data: string(data)})

		// Invalidate cached scan when disc changes.
		if ev.Type == drivemanager.EventDiscEjected || ev.Type == drivemanager.EventDiscInserted {
			orch.InvalidateScan(ev.DriveIndex)
		}

		// Clear drive session on eject so stale selection state doesn't persist.
		if ev.Type == drivemanager.EventDiscEjected && srv != nil {
			srv.ClearDriveSession(ev.DriveIndex)
		}

		// Broadcast drive-update with full drive list for dashboard Alpine store.
		allDrives := driveMgr.GetAllDrives()
		driveList := make([]web.DriveJSON, 0, len(allDrives))
		for _, dsm := range allDrives {
			driveList = append(driveList, web.DriveJSON{
				Index:    dsm.Index(),
				Name:     dsm.DriveName(),
				DiscName: dsm.DiscName(),
				State:    string(dsm.State()),
			})
		}
		driveUpdatePayload := struct {
			Ready bool           `json:"ready"`
			List  []web.DriveJSON `json:"list"`
		}{Ready: driveMgr.Ready(), List: driveList}
		driveUpdateData, _ := json.Marshal(driveUpdatePayload)
		sseHub.Broadcast(web.SSEEvent{Event: "drive-update", Data: string(driveUpdateData)})

		// Auto-rip: trigger on disc insert when enabled.
		if srv == nil {
			return
		}
		snapCfg := srv.GetConfig()
		if ev.Type == drivemanager.EventDiscInserted && snapCfg.AutoRip {
			go func() {
				slog.Info("auto-rip triggered", "drive_index", ev.DriveIndex, "disc_name", ev.DiscName)
				autoErr := orch.AutoRip(context.Background(), ev.DriveIndex, workflow.AutoRipConfig{
					OutputDir:       snapCfg.OutputDir,
					MovieTemplate:   snapCfg.MovieTemplate,
					SeriesTemplate:  snapCfg.SeriesTemplate,
					DuplicateAction: snapCfg.DuplicateAction,
				})
				if autoErr != nil {
					slog.Error("auto-rip failed", "error", autoErr, "drive_index", ev.DriveIndex)
				}
			}()
		}
	})

	// 12. Create web server with all dependencies.
	srv = web.NewServer(web.ServerDeps{
		Config:       &cfg,
		ConfigPath:   "/config/config.yaml",
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
