package contribute

// ReleaseInfo holds user-provided release metadata.
type ReleaseInfo struct {
	UPC        string `json:"upc"`
	RegionCode string `json:"region_code"`
	Year       int    `json:"year"`
	Format     string `json:"format"` // "Blu-ray", "UHD", "DVD"
	Slug       string `json:"slug"`   // e.g. "2024-blu-ray"
}

// TitleLabel holds the user's label for a single title.
type TitleLabel struct {
	TitleIndex int    `json:"title_index"`
	Type       string `json:"type"` // MainMovie, Episode, Extra, Trailer, DeletedScene
	Name       string `json:"name"`
	Season     string `json:"season"`
	Episode    string `json:"episode"`
	FileName   string `json:"file_name"`
}

// ReleaseJSON is the schema for TheDiscDB release.json.
type ReleaseJSON struct {
	Slug         string            `json:"Slug"`
	UPC          string            `json:"Upc,omitempty"`
	Year         int               `json:"Year"`
	Locale       string            `json:"Locale"`
	RegionCode   string            `json:"RegionCode"`
	Title        string            `json:"Title"`
	DateAdded    string            `json:"DateAdded"`
	Contributors []ContributorJSON `json:"Contributors"`
}

// ContributorJSON holds contributor metadata for TheDiscDB submissions.
type ContributorJSON struct {
	Name   string `json:"Name"`
	Source string `json:"Source"`
}

// DiscJSON is the schema for TheDiscDB disc01.json.
type DiscJSON struct {
	Index       int             `json:"Index"`
	Slug        string          `json:"Slug"`
	Name        string          `json:"Name"`
	Format      string          `json:"Format"`
	ContentHash string          `json:"ContentHash"`
	Titles      []DiscTitleJSON `json:"Titles"`
}

// DiscTitleJSON represents a single title entry in disc01.json.
type DiscTitleJSON struct {
	Index       int         `json:"Index"`
	Comment     string      `json:"Comment,omitempty"`
	SourceFile  string      `json:"SourceFile"`
	SegmentMap  string      `json:"SegmentMap"`
	Duration    string      `json:"Duration"`
	Size        int64       `json:"Size"`
	DisplaySize string      `json:"DisplaySize"`
	Tracks      []TrackJSON `json:"Tracks"`
}

// TrackJSON represents a single stream/track entry.
type TrackJSON struct {
	Index        int    `json:"Index"`
	Name         string `json:"Name"`
	Type         string `json:"Type"`
	Resolution   string `json:"Resolution,omitempty"`
	AspectRatio  string `json:"AspectRatio,omitempty"`
	AudioType    string `json:"AudioType,omitempty"`
	LanguageCode string `json:"LanguageCode,omitempty"`
	Language     string `json:"Language,omitempty"`
}
