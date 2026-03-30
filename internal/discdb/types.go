package discdb

// ExternalIDs holds external database identifiers for a media item.
type ExternalIDs struct {
	IMDB string `json:"imdb"`
	TMDB string `json:"tmdb"`
	TVDB string `json:"tvdb"`
}

// ContentItem represents a piece of content (movie/episode) associated with a disc title.
type ContentItem struct {
	Title   string `json:"title"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Type    string `json:"type"`
}

// DiscTitle represents a single title (playlist/track) on a disc.
type DiscTitle struct {
	ID          int          `json:"id"`
	Index       int          `json:"index"`
	SourceFile  string       `json:"sourceFile"`
	ItemType    string       `json:"itemType"`
	HasItem     bool         `json:"hasItem"`
	Duration    string       `json:"duration"`
	Size        string       `json:"size"`
	SegmentMap  string       `json:"segmentMap"`
	Season      int          `json:"season"`
	Episode     int          `json:"episode"`
	Item        *ContentItem `json:"item"`
}

// Disc represents a single physical disc within a release.
type Disc struct {
	ID     int         `json:"id"`
	Index  int         `json:"index"`
	Name   string      `json:"name"`
	Format string      `json:"format"`
	Slug   string      `json:"slug"`
	Titles []DiscTitle `json:"titles"`
}

// Release represents a specific physical release (e.g. Blu-ray edition) of a media item.
type Release struct {
	ID         int    `json:"id"`
	Title      string `json:"title"`
	Slug       string `json:"slug"`
	UPC        string `json:"upc"`
	ASIN       string `json:"asin"`
	ISBN       string `json:"isbn"`
	Year       int    `json:"year"`
	RegionCode string `json:"regionCode"`
	Locale     string `json:"locale"`
	ImageURL   string `json:"imageUrl"`
	Discs      []Disc `json:"discs"`
}

// MediaItem represents a movie or TV series in TheDiscDB.
type MediaItem struct {
	ID             int         `json:"id"`
	Title          string      `json:"title"`
	Slug           string      `json:"slug"`
	Year           int         `json:"year"`
	Type           string      `json:"type"`
	RuntimeMinutes int         `json:"runtimeMinutes"`
	ImageURL       string      `json:"imageUrl"`
	ExternalIDs    ExternalIDs `json:"externalids"`
	Releases       []Release   `json:"releases"`
}

// SearchResult bundles a media item with a matched release and disc.
type SearchResult struct {
	MediaItem MediaItem
	Release   Release
	Disc      Disc
}
