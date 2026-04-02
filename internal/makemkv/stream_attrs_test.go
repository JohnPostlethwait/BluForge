package makemkv

import (
	"testing"
)

// makeStream is a helper to build a StreamInfo with the given attributes.
func makeStream(titleIdx, streamIdx int, attrs map[int]string) StreamInfo {
	return StreamInfo{
		TitleIndex:  titleIdx,
		StreamIndex: streamIdx,
		Attributes:  attrs,
	}
}

// --- StreamInfo.Type() ---

func TestStreamType_Video(t *testing.T) {
	s := makeStream(0, 0, map[int]string{AttrType: "V_MPEG4/ISO/AVC"})
	if got := s.Type(); got != "video" {
		t.Errorf("expected video, got %q", got)
	}
}

func TestStreamType_Audio(t *testing.T) {
	s := makeStream(0, 1, map[int]string{AttrType: "A_TRUEHD"})
	if got := s.Type(); got != "audio" {
		t.Errorf("expected audio, got %q", got)
	}
}

func TestStreamType_Subtitle(t *testing.T) {
	s := makeStream(0, 2, map[int]string{AttrType: "S_HDMV/PGS"})
	if got := s.Type(); got != "subtitle" {
		t.Errorf("expected subtitle, got %q", got)
	}
}

func TestStreamType_Unknown(t *testing.T) {
	s := makeStream(0, 0, map[int]string{AttrType: "Chapters"})
	if got := s.Type(); got != "" {
		t.Errorf("expected empty string for unknown type, got %q", got)
	}
}

func TestStreamType_Missing(t *testing.T) {
	s := makeStream(0, 0, map[int]string{})
	if got := s.Type(); got != "" {
		t.Errorf("expected empty string when AttrType absent, got %q", got)
	}
}

// --- StreamInfo.IsVideo / IsAudio / IsSubtitle ---

func TestStreamIsVideo(t *testing.T) {
	s := makeStream(0, 0, map[int]string{AttrType: "V_MPEGH/ISO/HEVC"})
	if !s.IsVideo() {
		t.Error("expected IsVideo() == true")
	}
	if s.IsAudio() {
		t.Error("expected IsAudio() == false for video stream")
	}
	if s.IsSubtitle() {
		t.Error("expected IsSubtitle() == false for video stream")
	}
}

func TestStreamIsAudio(t *testing.T) {
	s := makeStream(0, 1, map[int]string{AttrType: "A_AC3"})
	if !s.IsAudio() {
		t.Error("expected IsAudio() == true")
	}
	if s.IsVideo() {
		t.Error("expected IsVideo() == false for audio stream")
	}
}

func TestStreamIsSubtitle(t *testing.T) {
	s := makeStream(0, 2, map[int]string{AttrType: "S_TEXT/UTF8"})
	if !s.IsSubtitle() {
		t.Error("expected IsSubtitle() == true")
	}
	if s.IsAudio() {
		t.Error("expected IsAudio() == false for subtitle stream")
	}
}

// --- StreamInfo attribute accessors ---

func TestStreamLangCode(t *testing.T) {
	s := makeStream(0, 1, map[int]string{AttrLangCode: "eng"})
	if got := s.LangCode(); got != "eng" {
		t.Errorf("expected eng, got %q", got)
	}
}

func TestStreamLangName(t *testing.T) {
	s := makeStream(0, 1, map[int]string{AttrLangName: "English"})
	if got := s.LangName(); got != "English" {
		t.Errorf("expected English, got %q", got)
	}
}

func TestStreamCodecShort(t *testing.T) {
	s := makeStream(0, 1, map[int]string{AttrCodecShort: "TrueHD"})
	if got := s.CodecShort(); got != "TrueHD" {
		t.Errorf("expected TrueHD, got %q", got)
	}
}

func TestStreamChannels(t *testing.T) {
	s := makeStream(0, 1, map[int]string{AttrChannels: "8"})
	if got := s.Channels(); got != "8" {
		t.Errorf("expected 8, got %q", got)
	}
}

// --- StreamInfo.IsDefault / IsForced ---

func TestStreamIsDefault_True(t *testing.T) {
	s := makeStream(0, 1, map[int]string{AttrMkvFlags: "d"})
	if !s.IsDefault() {
		t.Error("expected IsDefault() == true")
	}
	if s.IsForced() {
		t.Error("expected IsForced() == false")
	}
}

func TestStreamIsForced_True(t *testing.T) {
	s := makeStream(0, 2, map[int]string{AttrMkvFlags: "f"})
	if !s.IsForced() {
		t.Error("expected IsForced() == true")
	}
	if s.IsDefault() {
		t.Error("expected IsDefault() == false")
	}
}

func TestStreamIsBothDefaultAndForced(t *testing.T) {
	s := makeStream(0, 2, map[int]string{AttrMkvFlags: "df"})
	if !s.IsDefault() {
		t.Error("expected IsDefault() == true for df flag")
	}
	if !s.IsForced() {
		t.Error("expected IsForced() == true for df flag")
	}
}

func TestStreamFlags_None(t *testing.T) {
	s := makeStream(0, 1, map[int]string{})
	if s.IsDefault() {
		t.Error("expected IsDefault() == false when AttrMkvFlags absent")
	}
	if s.IsForced() {
		t.Error("expected IsForced() == false when AttrMkvFlags absent")
	}
}

// --- TitleInfo.AudioLanguages ---

func TestAudioLanguages_Basic(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			makeStream(0, 0, map[int]string{AttrType: "V_MPEG4/ISO/AVC", AttrLangCode: "eng"}),
			makeStream(0, 1, map[int]string{AttrType: "A_TRUEHD", AttrLangCode: "eng"}),
			makeStream(0, 2, map[int]string{AttrType: "A_AC3", AttrLangCode: "fra"}),
			makeStream(0, 3, map[int]string{AttrType: "S_HDMV/PGS", AttrLangCode: "eng"}),
		},
	}
	langs := title.AudioLanguages()
	if len(langs) != 2 {
		t.Fatalf("expected 2 audio languages, got %d: %v", len(langs), langs)
	}
	if langs[0] != "eng" {
		t.Errorf("expected langs[0]=eng, got %q", langs[0])
	}
	if langs[1] != "fra" {
		t.Errorf("expected langs[1]=fra, got %q", langs[1])
	}
}

func TestAudioLanguages_DedupesLangCode(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			makeStream(0, 0, map[int]string{AttrType: "A_TRUEHD", AttrLangCode: "eng"}),
			makeStream(0, 1, map[int]string{AttrType: "A_AC3", AttrLangCode: "eng"}), // same lang, different codec
		},
	}
	langs := title.AudioLanguages()
	if len(langs) != 1 {
		t.Errorf("expected deduped to 1 lang, got %d: %v", len(langs), langs)
	}
}

func TestAudioLanguages_Empty(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			makeStream(0, 0, map[int]string{AttrType: "V_MPEG4/ISO/AVC"}),
		},
	}
	langs := title.AudioLanguages()
	if len(langs) != 0 {
		t.Errorf("expected no audio langs, got %v", langs)
	}
}

func TestAudioLanguages_SkipsEmptyLangCode(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			makeStream(0, 0, map[int]string{AttrType: "A_TRUEHD"}), // no AttrLangCode
		},
	}
	langs := title.AudioLanguages()
	if len(langs) != 0 {
		t.Errorf("expected 0 langs when lang code absent, got %v", langs)
	}
}

// --- TitleInfo.SubtitleLanguages ---

func TestSubtitleLanguages_Basic(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			makeStream(0, 0, map[int]string{AttrType: "V_MPEG4/ISO/AVC"}),
			makeStream(0, 1, map[int]string{AttrType: "A_TRUEHD", AttrLangCode: "eng"}),
			makeStream(0, 2, map[int]string{AttrType: "S_HDMV/PGS", AttrLangCode: "eng"}),
			makeStream(0, 3, map[int]string{AttrType: "S_HDMV/PGS", AttrLangCode: "spa"}),
			makeStream(0, 4, map[int]string{AttrType: "S_TEXT/UTF8", AttrLangCode: "eng"}), // dup
		},
	}
	langs := title.SubtitleLanguages()
	if len(langs) != 2 {
		t.Fatalf("expected 2 subtitle langs, got %d: %v", len(langs), langs)
	}
	if langs[0] != "eng" {
		t.Errorf("expected langs[0]=eng, got %q", langs[0])
	}
	if langs[1] != "spa" {
		t.Errorf("expected langs[1]=spa, got %q", langs[1])
	}
}

func TestSubtitleLanguages_Empty(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams:    []StreamInfo{},
	}
	langs := title.SubtitleLanguages()
	if len(langs) != 0 {
		t.Errorf("expected no subtitle langs, got %v", langs)
	}
}

// --- TitleInfo.HasLosslessAudio ---

func TestHasLosslessAudio_TrueHD(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			makeStream(0, 0, map[int]string{AttrType: "V_MPEG4/ISO/AVC"}),
			makeStream(0, 1, map[int]string{AttrType: "A_TRUEHD", AttrCodecShort: "TrueHD"}),
			makeStream(0, 2, map[int]string{AttrType: "A_AC3", AttrCodecShort: "AC3"}),
		},
	}
	if !title.HasLosslessAudio() {
		t.Error("expected HasLosslessAudio() == true with TrueHD stream")
	}
}

func TestHasLosslessAudio_DTSHD(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			makeStream(0, 1, map[int]string{AttrType: "A_DTS", AttrCodecShort: "DTS-HD MA"}),
		},
	}
	if !title.HasLosslessAudio() {
		t.Error("expected HasLosslessAudio() == true with DTS-HD MA stream")
	}
}

func TestHasLosslessAudio_FLAC(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			makeStream(0, 1, map[int]string{AttrType: "A_FLAC", AttrCodecShort: "FLAC"}),
		},
	}
	if !title.HasLosslessAudio() {
		t.Error("expected HasLosslessAudio() == true with FLAC stream")
	}
}

func TestHasLosslessAudio_PCM(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			makeStream(0, 1, map[int]string{AttrType: "A_PCM/INT/LIT", AttrCodecShort: "PCM"}),
		},
	}
	if !title.HasLosslessAudio() {
		t.Error("expected HasLosslessAudio() == true with PCM stream")
	}
}

func TestHasLosslessAudio_LossyOnly(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			makeStream(0, 0, map[int]string{AttrType: "V_MPEG4/ISO/AVC"}),
			makeStream(0, 1, map[int]string{AttrType: "A_AC3", AttrCodecShort: "AC3"}),
			makeStream(0, 2, map[int]string{AttrType: "A_EAC3", AttrCodecShort: "E-AC3"}),
			makeStream(0, 3, map[int]string{AttrType: "A_DTS", AttrCodecShort: "DTS"}),
		},
	}
	if title.HasLosslessAudio() {
		t.Error("expected HasLosslessAudio() == false with lossy-only audio")
	}
}

func TestHasLosslessAudio_NoAudio(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			makeStream(0, 0, map[int]string{AttrType: "V_MPEG4/ISO/AVC"}),
		},
	}
	if title.HasLosslessAudio() {
		t.Error("expected HasLosslessAudio() == false with no audio streams")
	}
}

func TestHasLosslessAudio_EmptyStreams(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams:    []StreamInfo{},
	}
	if title.HasLosslessAudio() {
		t.Error("expected HasLosslessAudio() == false with empty streams")
	}
}
