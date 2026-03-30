package web

import (
	"encoding/json"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/templates"
)

// handleDashboard renders the dashboard with Alpine.js store data.
func (s *Server) handleDashboard(c echo.Context) error {
	drives := s.driveMgr.GetAllDrives()

	driveList := make([]DriveJSON, 0, len(drives))
	for _, dsm := range drives {
		driveList = append(driveList, DriveJSON{
			Index:    dsm.Index(),
			Name:     dsm.DriveName(),
			DiscName: dsm.DiscName(),
			State:    string(dsm.State()),
		})
	}

	storeData := DrivesStoreJSON{
		Ready: s.driveMgr.Ready(),
		List:  driveList,
	}
	storeBytes, _ := json.Marshal(storeData)

	data := templates.DashboardData{StoreJSON: string(storeBytes)}
	return templates.Dashboard(data).Render(c.Request().Context(), c.Response().Writer)
}
