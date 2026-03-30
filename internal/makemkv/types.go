package makemkv

// DriveInfo represents a physical disc drive detected by MakeMKV.
type DriveInfo struct {
	Index      int
	Visible    int
	Enabled    int
	Flags      int
	DriveName  string
	DiscName   string
	DevicePath string // e.g. "/dev/sr0"
}

// DiscInfo represents metadata about the disc currently in a drive.
type DiscInfo struct {
	Attributes map[int]string
}

// Name returns the disc name (attribute 2).
func (d *DiscInfo) Name() string {
	return d.Attributes[2]
}

// Type returns the disc type (attribute 1).
func (d *DiscInfo) Type() string {
	return d.Attributes[1]
}

// TitleInfo represents a single title (feature) on the disc.
type TitleInfo struct {
	Index      int
	Attributes map[int]string
	Streams    []StreamInfo
}

// Name returns the title name (attribute 2).
func (t *TitleInfo) Name() string {
	return t.Attributes[2]
}

// ChapterCount returns the chapter count string (attribute 8).
func (t *TitleInfo) ChapterCount() string {
	return t.Attributes[8]
}

// Duration returns the duration string (attribute 9).
func (t *TitleInfo) Duration() string {
	return t.Attributes[9]
}

// SizeHuman returns the human-readable size (attribute 10).
func (t *TitleInfo) SizeHuman() string {
	return t.Attributes[10]
}

// SizeBytes returns the size in bytes as a string (attribute 11).
func (t *TitleInfo) SizeBytes() string {
	return t.Attributes[11]
}

// Filename returns the output filename (attribute 27).
func (t *TitleInfo) Filename() string {
	return t.Attributes[27]
}

// SegmentMap returns the segment map (attribute 16).
func (t *TitleInfo) SegmentMap() string {
	return t.Attributes[16]
}

// SourceFile returns the source file path (attribute 33).
func (t *TitleInfo) SourceFile() string {
	return t.Attributes[33]
}

// StreamInfo represents a single audio, video, or subtitle stream within a title.
type StreamInfo struct {
	TitleIndex  int
	StreamIndex int
	Attributes  map[int]string
}

// Progress represents the current ripping progress reported by MakeMKV.
type Progress struct {
	Current int
	Total   int
	Max     int
}

// Message represents an informational or error message from MakeMKV.
type Message struct {
	Code   int
	Flags  int
	Count  int
	Text   string
	Format string
	Params []string
}

// Event is a parsed output line from makemkvcon robot mode.
type Event struct {
	Type     string
	Drive    *DriveInfo
	Disc     *DiscInfo
	Title    *TitleInfo
	Stream   *StreamInfo
	Progress *Progress
	Message  *Message
	Count    int
}
