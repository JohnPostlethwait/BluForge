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
	Contributor ContributorJSON `json:"contributor"`
	UPC         string          `json:"upc"`
	RegionCode  string          `json:"region_code"`
	Slug        string          `json:"slug"`
}

// ContributorJSON holds contributor metadata for TheDiscDB submissions.
type ContributorJSON struct {
	GitHub string `json:"github"`
}

// DiscJSON is the schema for TheDiscDB disc01.json.
type DiscJSON struct {
	Titles []DiscTitleJSON `json:"titles"`
}

// DiscTitleJSON represents a single title entry in disc01.json.
type DiscTitleJSON struct {
	Index        int        `json:"index"`
	Name         string     `json:"name"`
	Duration     string     `json:"duration"`
	ChapterCount string     `json:"chapter_count"`
	SizeHuman    string     `json:"size_human"`
	SizeBytes    string     `json:"size_bytes"`
	SourceFile   string     `json:"source_file"`
	Tracks       []TrackJSON `json:"tracks"`
}

// TrackJSON represents a single stream/track entry.
type TrackJSON struct {
	Type       string `json:"type"`
	CodecShort string `json:"codec_short,omitempty"`
	LangCode   string `json:"lang_code,omitempty"`
	LangName   string `json:"lang_name,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
}
