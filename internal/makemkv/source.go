package makemkv

import "fmt"

// SourceKind distinguishes the two things makemkvcon can operate on.
type SourceKind int

const (
	// SourceDisc is a physical drive, addressed as "disc:N".
	SourceDisc SourceKind = iota
	// SourceFile is a decrypted or backed-up disc folder, addressed as "file:<path>".
	SourceFile
)

// Source identifies what makemkvcon should read from. Scans and rips both take
// one, which is what lets a stripped backup folder stand in for a disc that
// MakeMKV refuses to open.
type Source struct {
	Kind       SourceKind
	DriveIndex int    // valid when Kind == SourceDisc
	Path       string // valid when Kind == SourceFile
}

// DiscSource returns a Source addressing the drive at driveIndex.
func DiscSource(driveIndex int) Source {
	return Source{Kind: SourceDisc, DriveIndex: driveIndex}
}

// FileSource returns a Source addressing a disc folder on disk.
func FileSource(path string) Source {
	return Source{Kind: SourceFile, Path: path}
}

// Arg renders the source as the argument makemkvcon expects.
func (s Source) Arg() string {
	if s.Kind == SourceFile {
		return "file:" + s.Path
	}
	return fmt.Sprintf("disc:%d", s.DriveIndex)
}

// IsDisc reports whether the source is a physical drive.
func (s Source) IsDisc() bool {
	return s.Kind == SourceDisc
}

// String makes Source readable in log output.
func (s Source) String() string { return s.Arg() }

// MakeMKV message codes that carry meaning for us. Codes are stable across
// releases; the accompanying text is localized and must never be matched on.
const (
	// MsgVolumeKeyUnknown — "The volume key is unknown for this disc - video
	// can't be decrypted".
	MsgVolumeKeyUnknown = 3303
	// MsgFailedToOpenDisc — "Failed to open disc".
	MsgFailedToOpenDisc = 5010
	// MsgNoUsableDrives — "The program can't find any usable optical drives".
	MsgNoUsableDrives = 5042
	// MsgSavingTitles — "Saving %1 titles into directory %2". The copy starting,
	// and the point at which reported progress begins to mean bytes written.
	MsgSavingTitles = 5014
	// MsgCopyFailed — "Copy complete. 0 titles saved, 1 failed." makemkvcon
	// still exits zero, so this is the only signal that a rip which appeared to
	// succeed produced nothing.
	MsgCopyFailed = 5037
)

// IsSpuriousAACSSignature reports whether a scan result carries the fingerprint
// of a disc whose AACS directory is present but whose payload may be
// unencrypted: a volume-key failure, a failure to open, and no titles at all.
//
// This is only a *candidate* signal. It is identical to the signature of a disc
// with a genuinely unknown volume key, so a caller must inspect the payload
// before acting on it — see internal/aacs.
func IsSpuriousAACSSignature(messages []Message, titleCount int) bool {
	if titleCount != 0 {
		return false
	}
	return hasMessage(messages, MsgVolumeKeyUnknown) && hasMessage(messages, MsgFailedToOpenDisc)
}

// HasNoDrivesMessage reports whether MSG:5042 appears in messages.
//
// This is meaningful only when the source is a disc. makemkvcon emits 5042 on
// nearly every invocation — including successful ones and any operation on a
// file source — so treating it as a general error produces false alarms.
func HasNoDrivesMessage(messages []Message) bool {
	return hasMessage(messages, MsgNoUsableDrives)
}

func hasMessage(messages []Message, code int) bool {
	for _, m := range messages {
		if m.Code == code {
			return true
		}
	}
	return false
}

// MessageCodes returns the codes present in messages, in order. Logged on every
// zero-title scan so that a signature variant is diagnosable from logs alone
// rather than requiring the investigation to be repeated.
func MessageCodes(messages []Message) []int {
	codes := make([]int, 0, len(messages))
	for _, m := range messages {
		codes = append(codes, m.Code)
	}
	return codes
}

// ScanError reports a scan that produced no usable titles, retaining the
// partially parsed scan so callers can inspect the messages makemkvcon emitted.
// A bare error would discard exactly the information needed to tell a
// spurious-AACS failure from any other open failure.
type ScanError struct {
	Scan   *DiscScan
	Source Source
	Reason string
	Err    error
}

func (e *ScanError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("makemkv: scan %s: %s", e.Source.Arg(), e.Reason)
	}
	if e.Err != nil {
		return fmt.Sprintf("makemkv: scan %s: %v", e.Source.Arg(), e.Err)
	}
	return fmt.Sprintf("makemkv: scan %s failed", e.Source.Arg())
}

// Unwrap exposes the underlying command error to errors.Is/errors.As.
func (e *ScanError) Unwrap() error { return e.Err }

// Messages returns the messages carried by the failed scan, or nil.
func (e *ScanError) Messages() []Message {
	if e.Scan == nil {
		return nil
	}
	return e.Scan.Messages
}
