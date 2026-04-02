package makemkv

import "strings"

// SINFO attribute ID constants.
const (
	AttrType         = 1  // Stream type (value: codec string like "V_MPEG4/ISO/AVC", "A_TRUEHD", "S_HDMV/PGS")
	AttrName         = 2  // Name/description
	AttrLangCode     = 3  // ISO 639-2 language code (e.g., "eng", "fra")
	AttrLangName     = 4  // Human-readable language name
	AttrCodecID      = 5  // Codec ID (e.g., "A_TRUEHD")
	AttrCodecShort   = 6  // Codec short name (e.g., "TrueHD")
	AttrChapterCount = 8
	AttrDuration     = 9
	AttrSizeHuman    = 10
	AttrSizeBytes    = 11
	AttrBitrate      = 13
	AttrChannels     = 14 // Audio channel count
	AttrSourceFile   = 16
	AttrOutputFile   = 27
	AttrMkvFlags     = 38 // "d" (default), "f" (forced), "df" (both)
)


// losslessCodecs is the set of codec short names considered lossless.
var losslessCodecs = map[string]bool{
	"TrueHD":   true,
	"DTS-HD MA": true,
	"FLAC":     true,
	"PCM":      true,
}

// Type returns the stream type as "video", "audio", or "subtitle".
// The type is inferred from the codec string stored in attribute 1, which uses
// the Matroska codec ID prefix convention: "V_" for video, "A_" for audio,
// "S_" for subtitle.
func (s *StreamInfo) Type() string {
	codec := s.Attributes[AttrType]
	switch {
	case strings.HasPrefix(codec, "V_"):
		return "video"
	case strings.HasPrefix(codec, "A_"):
		return "audio"
	case strings.HasPrefix(codec, "S_"):
		return "subtitle"
	default:
		return ""
	}
}

// LangCode returns the ISO 639-2 language code (attribute 3).
func (s *StreamInfo) LangCode() string {
	return s.Attributes[AttrLangCode]
}

// LangName returns the human-readable language name (attribute 4).
func (s *StreamInfo) LangName() string {
	return s.Attributes[AttrLangName]
}

// CodecShort returns the short codec name (attribute 6), e.g. "TrueHD", "AC3".
func (s *StreamInfo) CodecShort() string {
	return s.Attributes[AttrCodecShort]
}

// Channels returns the audio channel count string (attribute 14).
func (s *StreamInfo) Channels() string {
	return s.Attributes[AttrChannels]
}

// IsDefault reports whether this stream is flagged as default (attribute 38 contains "d").
func (s *StreamInfo) IsDefault() bool {
	return strings.Contains(s.Attributes[AttrMkvFlags], "d")
}

// IsForced reports whether this stream is flagged as forced (attribute 38 contains "f").
// Note: "df" satisfies both IsDefault and IsForced.
func (s *StreamInfo) IsForced() bool {
	return strings.Contains(s.Attributes[AttrMkvFlags], "f")
}

// IsVideo reports whether this is a video stream.
func (s *StreamInfo) IsVideo() bool {
	return s.Type() == "video"
}

// IsAudio reports whether this is an audio stream.
func (s *StreamInfo) IsAudio() bool {
	return s.Type() == "audio"
}

// IsSubtitle reports whether this is a subtitle stream.
func (s *StreamInfo) IsSubtitle() bool {
	return s.Type() == "subtitle"
}

// AudioLanguages returns the unique ISO 639-2 language codes for all audio streams
// in the title, in the order they first appear.
func (t *TitleInfo) AudioLanguages() []string {
	seen := make(map[string]bool)
	var langs []string
	for i := range t.Streams {
		s := &t.Streams[i]
		if s.IsAudio() {
			if lc := s.LangCode(); lc != "" && !seen[lc] {
				seen[lc] = true
				langs = append(langs, lc)
			}
		}
	}
	return langs
}

// SubtitleLanguages returns the unique ISO 639-2 language codes for all subtitle
// streams in the title, in the order they first appear.
func (t *TitleInfo) SubtitleLanguages() []string {
	seen := make(map[string]bool)
	var langs []string
	for i := range t.Streams {
		s := &t.Streams[i]
		if s.IsSubtitle() {
			if lc := s.LangCode(); lc != "" && !seen[lc] {
				seen[lc] = true
				langs = append(langs, lc)
			}
		}
	}
	return langs
}

// HasLosslessAudio reports whether any audio stream in the title uses a lossless codec.
// Lossless codecs: TrueHD, DTS-HD MA, FLAC, PCM.
func (t *TitleInfo) HasLosslessAudio() bool {
	for i := range t.Streams {
		s := &t.Streams[i]
		if s.IsAudio() && losslessCodecs[s.CodecShort()] {
			return true
		}
	}
	return false
}
