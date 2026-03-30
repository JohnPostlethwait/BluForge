package web

import (
	"strings"

	"github.com/labstack/echo/v4"
)

// wantsJSON returns true if the request's Accept header contains "application/json".
func wantsJSON(c echo.Context) bool {
	return strings.Contains(c.Request().Header.Get("Accept"), "application/json")
}

// DriveJSON is the JSON representation of a drive for Alpine.js stores.
type DriveJSON struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	DiscName string `json:"discName"`
	State    string `json:"state"`
}

// TitleJSON is the JSON representation of a disc title for Alpine.js stores.
type TitleJSON struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	SourceFile string `json:"sourceFile"`
	Selected   bool   `json:"selected"`
}

// SelectedReleaseJSON is the JSON representation of a user-selected release.
type SelectedReleaseJSON struct {
	MediaItemID string `json:"mediaItemID"`
	ReleaseID   string `json:"releaseID"`
	Title       string `json:"title"`
	Year        string `json:"year"`
	Type        string `json:"type"`
}

// SearchResultJSON is the JSON representation of a search result row.
type SearchResultJSON struct {
	MediaTitle   string `json:"mediaTitle"`
	MediaYear    int    `json:"mediaYear"`
	MediaType    string `json:"mediaType"`
	ReleaseTitle string `json:"releaseTitle"`
	ReleaseUPC   string `json:"releaseUPC"`
	ReleaseASIN  string `json:"releaseASIN"`
	RegionCode   string `json:"regionCode"`
	Format       string `json:"format"`
	DiscCount    int    `json:"discCount"`
	ReleaseID    string `json:"releaseID"`
	MediaItemID  string `json:"mediaItemID"`
}

// DriveStoreJSON is the full Alpine.store('drive') shape for the drive detail page.
type DriveStoreJSON struct {
	DriveIndex      int                  `json:"driveIndex"`
	DriveName       string               `json:"driveName"`
	DiscName        string               `json:"discName"`
	State           string               `json:"state"`
	Scanning        bool                 `json:"scanning"`
	Titles          []TitleJSON          `json:"titles"`
	SelectedRelease *SelectedReleaseJSON `json:"selectedRelease"`
	SearchResults   []SearchResultJSON   `json:"searchResults"`
}

// DrivesStoreJSON is the Alpine.store('drives') shape for the dashboard page.
type DrivesStoreJSON struct {
	Ready bool        `json:"ready"`
	List  []DriveJSON `json:"list"`
}
