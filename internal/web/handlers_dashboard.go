package web

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/templates"
)

// DriveListJSON describes every known drive for the dashboard.
//
// The page render and the drive-update SSE event both need this, and they used
// to build it separately. The SSE copy set only index, name, disc and state,
// leaving RipProgress and WorkflowStep at their zero values — which the page
// reads as "ripping, at 0%" and "no disc". Every insert or eject therefore
// rewrote every card on the dashboard, blanking the progress of a rip running
// on a completely different drive. One builder is what keeps the two in step.
func (s *Server) DriveListJSON() []DriveJSON {
	drives := s.driveMgr.GetAllDrives()

	// Build active job lookup for per-drive progress.
	activeByDrive := make(map[int]int) // driveIndex -> progress
	if s.ripEngine != nil {
		for _, j := range s.ripEngine.ActiveJobs() {
			snap := j.Snapshot()
			activeByDrive[snap.DriveIndex] = snap.Progress
		}
	}

	driveList := make([]DriveJSON, 0, len(drives))
	for _, dsm := range drives {
		idx := dsm.Index()
		dj := DriveJSON{
			Index:        idx,
			Name:         dsm.DriveName(),
			DiscName:     dsm.DiscName(),
			State:        string(dsm.State()),
			RipProgress:  -1,
			WorkflowStep: 0,
		}

		// Compute workflow step for this drive.
		if dsm.DiscName() != "" {
			dj.WorkflowStep = 1 // has disc, at least step 1
			if session, ok := s.driveSessions.Snapshot(idx); ok {
				if len(session.SearchResults) > 0 {
					dj.WorkflowStep = 2
				}
				if session.ReleaseID != "" {
					dj.WorkflowStep = 3
				}
			}
			if s.orchestrator != nil {
				if scan := s.orchestrator.GetCachedScanByDrive(idx); scan != nil {
					if dj.WorkflowStep < 4 {
						dj.WorkflowStep = 4
					}
				}
			}
		}

		if progress, ok := activeByDrive[idx]; ok {
			dj.RipProgress = progress
			dj.WorkflowStep = 5
		}

		driveList = append(driveList, dj)
	}

	return driveList
}

// handleDashboard renders the dashboard with Alpine.js store data.
func (s *Server) handleDashboard(c echo.Context) error {
	driveList := s.DriveListJSON()

	var activeJobs []*ripper.Job
	var queuedJobs []*ripper.Job
	if s.ripEngine != nil {
		activeJobs = s.ripEngine.ActiveJobs()
		queuedJobs = s.ripEngine.QueuedJobs()
	}

	// Completed today count.
	completedToday := 0
	if s.store != nil {
		if count, err := s.store.CountJobsCompletedToday(); err == nil {
			completedToday = count
		}
	}

	// Recent jobs (last 5).
	recentJobs := make([]DashboardJobJSON, 0, 5)
	if s.store != nil {
		if dbJobs, err := s.store.ListAllJobs(5, 0); err == nil {
			for _, j := range dbJobs {
				recentJobs = append(recentJobs, DashboardJobJSON{
					ID:         j.ID,
					DiscName:   j.DiscName,
					TitleName:  j.TitleName,
					Status:     j.Status,
					FinishedAt: j.UpdatedAt.Format("Jan 2 15:04"),
				})
			}
		}
	}

	storeData := DrivesStoreJSON{
		Ready:          s.driveMgr.Ready(),
		List:           driveList,
		ActiveCount:    len(activeJobs),
		QueuedCount:    len(queuedJobs),
		CompletedToday: completedToday,
		RecentJobs:     recentJobs,
	}

	// A page that lost its event stream has no way to catch up: events are
	// delivered once and never replayed. This is the same store the render
	// embeds, served so the page can ask again on reconnect.
	if wantsJSON(c) {
		return c.JSON(http.StatusOK, storeData)
	}

	storeBytes, err := json.Marshal(storeData)
	if err != nil {
		slog.Error("failed to marshal dashboard store", "error", err)
	}

	data := templates.DashboardData{StoreJSON: string(storeBytes)}
	return templates.Dashboard(data).Render(c.Request().Context(), c.Response().Writer)
}
