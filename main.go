package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
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

	// 2b. Set up MakeMKV data directory, registration key, and settings.
	setupMakeMKVData("/config", cfg.MinTitleLength)

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
	org := organizer.New()

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
		driveUpdateData, err := json.Marshal(driveUpdatePayload)
		if err != nil {
			slog.Error("failed to marshal drive update payload", "err", err)
			return
		}
		sseHub.Broadcast(web.SSEEvent{Event: "drive-update", Data: string(driveUpdateData)})

		// Auto-rip: trigger on disc insert when enabled.
		if srv == nil {
			return
		}
		snapCfg := srv.GetConfig()
		if ev.Type == drivemanager.EventDiscInserted && snapCfg.AutoRip {
			go func() {
				slog.Info("auto-rip triggered", "drive_index", ev.DriveIndex, "disc_name", ev.DiscName)

				// Build selection opts from config defaults.
				var selOpts *makemkv.SelectionOpts
				if snapCfg.PreferredAudioLangs != "" || snapCfg.PreferredSubtitleLangs != "" {
					selOpts = &makemkv.SelectionOpts{
						KeepForced:   snapCfg.KeepForcedSubtitles,
						KeepLossless: snapCfg.KeepLosslessAudio,
					}
					if snapCfg.PreferredAudioLangs != "" {
						selOpts.AudioLangs = strings.Split(snapCfg.PreferredAudioLangs, ",")
					}
					if snapCfg.PreferredSubtitleLangs != "" {
						selOpts.SubtitleLangs = strings.Split(snapCfg.PreferredSubtitleLangs, ",")
					}
				}

				autoErr := orch.AutoRip(context.Background(), ev.DriveIndex, workflow.AutoRipConfig{
					OutputDir:       snapCfg.OutputDir,
					DuplicateAction: snapCfg.DuplicateAction,
					SelectionOpts:   selOpts,
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

// setupMakeMKVData ensures MakeMKV's data directory (~/.MakeMKV) is persisted
// under the given configDir so AACS keys, SDF/LibreDrive data
// (_private_data.tar), and settings survive container restarts.
//
// It also writes required settings to settings.conf:
//   - app_Key: registration key from MAKEMKV_KEY env var (required for BD/UHD)
//   - app_DataDir: persistent data directory for SDF.bin and hashed keys
//   - dvd_MinimumTitleLength: title length filter (applies to BD too, despite the name)
func setupMakeMKVData(configDir string, minTitleLength int) {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/root"
	}

	homeMakeMKV := filepath.Join(home, ".MakeMKV")
	persistDir := filepath.Join(configDir, ".MakeMKV")

	// Create the persistent directory if it doesn't exist.
	if err := os.MkdirAll(persistDir, 0755); err != nil {
		slog.Error("failed to create MakeMKV data dir", "path", persistDir, "error", err)
		return
	}

	// Symlink ~/.MakeMKV → /config/.MakeMKV (skip if already correct).
	target, err := os.Readlink(homeMakeMKV)
	if err == nil && target == persistDir {
		// Already symlinked correctly.
	} else {
		// Remove whatever is there (old dir or broken symlink) and create symlink.
		if err := os.RemoveAll(homeMakeMKV); err != nil {
			slog.Warn("failed to remove existing MakeMKV home dir", "path", homeMakeMKV, "err", err)
		}
		if err := os.Symlink(persistDir, homeMakeMKV); err != nil {
			slog.Error("failed to symlink MakeMKV data dir", "from", homeMakeMKV, "to", persistDir, "error", err)
			return
		}
	}
	slog.Info("MakeMKV data dir configured", "path", persistDir)

	// Build the settings we need to ensure are present.
	required := map[string]string{
		"app_DataDir":            persistDir,
		"app_UpdateEnable":       "1",
		"dvd_MinimumTitleLength": fmt.Sprintf("%d", minTitleLength),
	}
	if key := os.Getenv("MAKEMKV_KEY"); key != "" {
		required["app_Key"] = key
	}

	settingsPath := filepath.Join(persistDir, "settings.conf")
	writeMakeMKVSettings(settingsPath, required)
}

// writeMakeMKVSettings merges the given key/value pairs into a MakeMKV
// settings.conf file, preserving all other existing settings.
func writeMakeMKVSettings(path string, settings map[string]string) {
	// Read existing settings (may not exist yet).
	var lines []string
	written := make(map[string]bool)

	if f, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			// Check if this line matches any setting we want to set.
			replaced := false
			for key, val := range settings {
				prefix := key + " "
				if strings.HasPrefix(line, prefix) {
					lines = append(lines, fmt.Sprintf(`%s = "%s"`, key, val))
					written[key] = true
					replaced = true
					break
				}
			}
			if !replaced {
				lines = append(lines, line)
			}
		}
		f.Close()
	}

	// Append any settings that weren't already in the file.
	for key, val := range settings {
		if !written[key] {
			lines = append(lines, fmt.Sprintf(`%s = "%s"`, key, val))
		}
	}

	var buf strings.Builder
	for _, l := range lines {
		buf.WriteString(l)
		buf.WriteByte('\n')
	}

	if err := os.WriteFile(path, []byte(buf.String()), 0600); err != nil {
		slog.Error("failed to write MakeMKV settings", "path", path, "error", err)
		return
	}

	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	slog.Info("MakeMKV settings configured", "path", path, "keys", keys)
}
