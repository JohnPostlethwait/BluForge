package makemkv

// Drive states as reported in the DRV line's second field. These are
// AP_DriveState from MakeMKV's apdefs.h.
//
// The published field list at makemkv.com/developers/usage.txt calls this field
// "set to 1 if drive is present". That is stale — the same document lists six
// DRV fields where makemkvcon emits seven — and it does not describe observed
// output, where an empty drive reports 0 and an absent slot reports 256.
const (
	DriveStateEmptyClosed = 0
	DriveStateEmptyOpen   = 1
	DriveStateInserted    = 2
	DriveStateLoading     = 3
	DriveStateNoDrive     = 256
	DriveStateUnmounting  = 257
)

// DriveInfo represents a physical disc drive detected by MakeMKV.
type DriveInfo struct {
	Index int
	// State is MakeMKV's own account of the drive, one of the DriveState
	// constants above. It is the only field that says outright whether the slot
	// holds hardware and whether that hardware holds a disc.
	State      int
	Flags      int
	DriveName  string
	DiscName   string
	DevicePath string // e.g. "/dev/sr0"
}

// Present reports that this slot has a drive attached.
//
// makemkvcon lists every slot it could use, including ones with no hardware, as
// DRV:N,256,999,0,"","","". Those have to be skipped — but deciding it from the
// blank drive name means a real drive that momentarily reports no name is taken
// for an empty slot, and then for a drive that has been unplugged.
//
// The device path is a second witness: a real drive has one even in a poll that
// omits its name, and a phantom slot never does.
func (d DriveInfo) Present() bool {
	if d.State == DriveStateNoDrive {
		return false
	}
	return d.DriveName != "" || d.DevicePath != ""
}

// HasDisc reports that the drive currently holds readable media.
//
// Read from the state rather than inferred from the disc name and media flags.
// A disc with no volume label reports an empty name, and inferring from that
// showed an empty drive with a disc sitting in it — nothing could be scanned or
// ripped, because as far as BluForge was concerned there was nothing there.
func (d DriveInfo) HasDisc() bool {
	return d.State == DriveStateInserted
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
// For UHD/4K discs this contains the MPLS playlist filename (e.g. "00300.mpls").
// For standard Blu-ray it may contain segment numbers (e.g. "1,2,3").
func (t *TitleInfo) SegmentMap() string {
	return t.Attributes[16]
}

// SourceFile returns the source playlist/file name used for matching against
// TheDiscDB. This is attribute 16 (the MPLS playlist, e.g. "00300.mpls"),
// NOT attribute 33 (which is the device path or drive index).
func (t *TitleInfo) SourceFile() string {
	return t.Attributes[16]
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
	// Operation is the name makemkvcon gives the step it is on, from a
	// PRGT/PRGC line. It is the only thing a long scan says about itself.
	Operation string
}
