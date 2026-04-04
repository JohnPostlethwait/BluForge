// Package mpls reads stream language codes from Blu-ray MPLS playlist files.
// MPLS files (BDMV/BACKUP/PLAYLIST/*.mpls) are the authoritative source for
// stream language metadata on Blu-ray and UHD discs; CLPI clip-info files
// often omit language codes, especially on UHD disc authorings.
package mpls

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// StreamEntry holds the language code and codec metadata for a single stream
// as extracted from an MPLS PlayItem's STN_Table.
type StreamEntry struct {
	LangCode   string // ISO 639-2 language code; "" = not set
	CodingType byte   // MPLS coding_type byte (e.g. 0x83 = TrueHD)
}

// PlayItemLanguages holds the ordered stream metadata for one PlayItem
// (one logical title) in an MPLS playlist.
type PlayItemLanguages struct {
	Audio    []StreamEntry // audio streams in playlist order
	Subtitle []StreamEntry // subtitle streams in playlist order
}

// CodingTypeToCodecID maps an MPLS coding_type byte to the Matroska-style codec
// ID prefix that MakeMKV uses (e.g. "A_TRUEHD"). Returns "" for unknown types.
func CodingTypeToCodecID(ct byte) string {
	switch ct {
	case 0x03:
		return "A_MPEG/L1"
	case 0x04:
		return "A_MPEG/L2"
	case 0x80:
		return "A_LPCM"
	case 0x81:
		return "A_AC3"
	case 0x82:
		return "A_DTS"
	case 0x83:
		return "A_TRUEHD"
	case 0x84:
		return "A_EAC3"
	case 0x85, 0x86:
		return "A_DTSHD"
	case 0xA1:
		return "A_EAC3"
	case 0xA2:
		return "A_DTSHD"
	case 0x90:
		return "S_HDMV/PGS"
	case 0x91:
		return "S_HDMV/IGS"
	case 0x92:
		return "S_TEXT/UTF8"
	default:
		return ""
	}
}

// CodingTypeToCodecShort maps an MPLS coding_type byte to the short codec name
// that MakeMKV uses for display (e.g. "TrueHD"). Returns "" for unknown types.
func CodingTypeToCodecShort(ct byte) string {
	switch ct {
	case 0x03:
		return "MPEG Audio"
	case 0x04:
		return "MPEG Audio"
	case 0x80:
		return "PCM"
	case 0x81:
		return "AC3"
	case 0x82:
		return "DTS"
	case 0x83:
		return "TrueHD"
	case 0x84:
		return "E-AC3"
	case 0x85, 0x86:
		return "DTS-HD MA"
	case 0xA1:
		return "E-AC3"
	case 0xA2:
		return "DTS-HD"
	case 0x90:
		return "PGS"
	case 0x91:
		return "IGS"
	case 0x92:
		return "SRT"
	default:
		return ""
	}
}

// ParseMPLS parses a Blu-ray MPLS binary file and returns language codes for
// each PlayItem.  The returned slice is indexed by PlayItem order; most movie
// MPLS files contain exactly one PlayItem.
func ParseMPLS(data []byte) ([]PlayItemLanguages, error) {
	if len(data) < 20 {
		return nil, fmt.Errorf("mpls: file too short (%d bytes)", len(data))
	}
	if string(data[0:4]) != "MPLS" {
		return nil, fmt.Errorf("mpls: invalid magic %q", data[0:4])
	}

	playListOffset := int(binary.BigEndian.Uint32(data[12:16]))
	if playListOffset+10 > len(data) {
		return nil, fmt.Errorf("mpls: PlayList offset %d out of range", playListOffset)
	}

	pl := data[playListOffset:]
	// pl[0:4]  = length of PlayList section (not including this field)
	// pl[4:6]  = reserved
	// pl[6:8]  = number_of_PlayItems
	// pl[8:10] = number_of_SubPaths
	numItems := int(binary.BigEndian.Uint16(pl[6:8]))

	pos := 10 // offset into pl where PlayItem list begins
	result := make([]PlayItemLanguages, 0, numItems)

	for i := 0; i < numItems; i++ {
		if pos+2 > len(pl) {
			break
		}
		itemLen := int(binary.BigEndian.Uint16(pl[pos : pos+2]))
		itemEnd := pos + 2 + itemLen
		if itemEnd > len(pl) {
			break
		}
		item := pl[pos+2 : itemEnd] // PlayItem data, excluding the 2-byte length field
		langs, err := parsePlayItemLanguages(item)
		if err != nil {
			result = append(result, PlayItemLanguages{})
		} else {
			result = append(result, langs)
		}
		pos = itemEnd
	}

	return result, nil
}

// parsePlayItemLanguages extracts audio and subtitle language codes from a
// single PlayItem's binary data (the bytes after the 2-byte length field).
func parsePlayItemLanguages(item []byte) (PlayItemLanguages, error) {
	// Minimum PlayItem data required to reach still_mode at byte 29.
	if len(item) < 30 {
		return PlayItemLanguages{}, fmt.Errorf("mpls: PlayItem too short (%d bytes)", len(item))
	}

	// Layout of item[]:
	//   [0:5]   Clip_Information_file_name (5 bytes ASCII)
	//   [5:9]   Clip_codec_identifier      (4 bytes ASCII, e.g. "M2TS")
	//   [9:11]  reserved(11bits)|is_multi_angle(1bit)|connection_condition(4bits)
	//   [11]    ref_to_STC_id
	//   [12:16] IN_time
	//   [16:20] OUT_time
	//   [20:28] UO_mask_table
	//   [28]    random_access_flag|reserved
	//   [29]    still_mode
	//   [30...] still_time (2 bytes, only when still_mode==1)
	//           multi-angle data (when is_multi_angle==1)
	//   [stnOffset...] STN_Table

	// is_multi_angle is bit 4 of item[10] (the low byte of the 16-bit flags field).
	isMultiAngle := (item[10] >> 4) & 1
	stillMode := item[29]

	stnOffset := 30
	if stillMode == 1 {
		stnOffset += 2 // still_time is present only for finite-still mode
	}

	if isMultiAngle == 1 {
		if stnOffset >= len(item) {
			return PlayItemLanguages{}, fmt.Errorf("mpls: multi-angle data out of bounds")
		}
		numAngles := int(item[stnOffset])
		// number_of_angles (1 byte) + flags byte (1 byte) + (numAngles-1) * 10 bytes each.
		// The primary angle is already encoded in the PlayItem header; only the
		// additional angles have separate 10-byte entries here.
		stnOffset += 2 + (numAngles-1)*10
	}

	return parseSTNTable(item, stnOffset)
}

// parseSTNTable reads the STN_Table (Stream Number Table) starting at stnOffset
// within item and returns audio/subtitle language codes.
func parseSTNTable(item []byte, stnOffset int) (PlayItemLanguages, error) {
	// STN_Table minimum header is 16 bytes.
	if stnOffset+16 > len(item) {
		return PlayItemLanguages{}, fmt.Errorf("mpls: STN_Table at offset %d out of bounds", stnOffset)
	}

	stn := item[stnOffset:]
	// stn[0:2]  = length (of everything after these 2 bytes)
	// stn[2:4]  = reserved
	// stn[4]    = number_of_video_stream_entries
	// stn[5]    = number_of_audio_stream_entries
	// stn[6]    = number_of_PG_stream_entries   (PG = Presentation Graphics = subtitles)
	// stn[7]    = number_of_IG_stream_entries
	// stn[8]    = number_of_secondary_audio_stream_entries
	// stn[9]    = number_of_secondary_video_stream_entries
	// stn[10]   = number_of_secondary_PG_stream_entries
	// stn[11:16]= reserved
	// stn[16:]  = stream entries
	nVideo := int(stn[4])
	nAudio := int(stn[5])
	nPG := int(stn[6])

	pos := 16 // stream entries begin here within stn

	// Skip past video stream entries (video has no language code).
	for i := 0; i < nVideo; i++ {
		pos = skipStreamEntry(stn, pos)
	}

	// Parse audio stream entries.
	audio := make([]StreamEntry, 0, nAudio)
	for i := 0; i < nAudio; i++ {
		lang, ct, next := parseStreamEntryLang(stn, pos)
		audio = append(audio, StreamEntry{LangCode: lang, CodingType: ct})
		pos = next
	}

	// Parse PG (subtitle) stream entries.
	subtitle := make([]StreamEntry, 0, nPG)
	for i := 0; i < nPG; i++ {
		lang, ct, next := parseStreamEntryLang(stn, pos)
		subtitle = append(subtitle, StreamEntry{LangCode: lang, CodingType: ct})
		pos = next
	}

	return PlayItemLanguages{Audio: audio, Subtitle: subtitle}, nil
}

// skipStreamEntry advances pos past one stream entry and its StreamCodingInfo
// without extracting any data.
func skipStreamEntry(data []byte, pos int) int {
	if pos >= len(data) {
		return pos
	}
	// StreamEntry: 1-byte length field + that many bytes of data.
	entryLen := int(data[pos])
	pos += 1 + entryLen
	if pos >= len(data) {
		return pos
	}
	// StreamCodingInfo: 1-byte length field + that many bytes of data.
	ciLen := int(data[pos])
	return pos + 1 + ciLen
}

// parseStreamEntryLang reads one stream entry and its StreamCodingInfo,
// extracting the language code (ISO 639-2, lowercase) and coding type byte.
// Returns ("", 0, next) for video streams or streams with no language metadata.
func parseStreamEntryLang(data []byte, pos int) (string, byte, int) {
	if pos >= len(data) {
		return "", 0, pos
	}

	// Skip the StreamEntry (PID, stream_type, etc. — not needed for language).
	entryLen := int(data[pos])
	pos += 1 + entryLen

	if pos >= len(data) {
		return "", 0, pos
	}

	// Parse StreamCodingInfo.
	ciLen := int(data[pos])
	ciEnd := pos + 1 + ciLen
	pos++ // advance past the ciLen byte

	if pos >= len(data) {
		return "", 0, ciEnd
	}

	codingType := data[pos]
	pos++ // advance past coding_type; pos now points to type-specific payload

	var rawLang []byte
	switch {
	case isAudioCodingType(codingType):
		// Payload: audio_format(4bits)+sample_rate(4bits) = 1 byte, then language_code(3 bytes).
		if pos+4 <= ciEnd && pos+4 <= len(data) {
			rawLang = data[pos+1 : pos+4]
		}
	case codingType == 0x90 || codingType == 0x91:
		// PG (Presentation Graphics) or IG (Interactive Graphics).
		// Payload: language_code(3 bytes) directly.
		if pos+3 <= ciEnd && pos+3 <= len(data) {
			rawLang = data[pos : pos+3]
		}
	case codingType == 0x92:
		// Text subtitle.
		// Payload: char_code(1 byte) then language_code(3 bytes).
		if pos+4 <= ciEnd && pos+4 <= len(data) {
			rawLang = data[pos+1 : pos+4]
		}
		// Video coding types (0x01, 0x02, 0x1B, 0x24, 0xEA) carry no language; fall through.
	}

	lang := decodeLang(rawLang)
	return lang, codingType, ciEnd
}

// decodeLang converts raw MPLS language bytes to a lowercase ISO 639-2 string.
// Returns "" for null/zero bytes or the "und" (undetermined) code.
func decodeLang(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	s := strings.TrimRight(string(b), "\x00")
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "und" || s == "\xff\xff\xff" {
		return ""
	}
	return s
}

// isAudioCodingType reports whether the given MPLS coding_type byte indicates
// an audio stream (as opposed to video or subtitle).
func isAudioCodingType(t byte) bool {
	// Defined audio coding types in the Blu-ray spec (BDMV format):
	//   0x03 MPEG-1 Audio
	//   0x04 MPEG-2 Audio
	//   0x80 LPCM
	//   0x81 Dolby AC-3
	//   0x82 DTS
	//   0x83 Dolby TrueHD
	//   0x84 Dolby Digital Plus (E-AC-3)
	//   0x85 DTS-HD High Resolution / DTS-HD Master Audio
	//   0x86 DTS-HD Master Audio
	//   0xA1 E-AC-3 Secondary Audio
	//   0xA2 DTS-HD Secondary Audio
	switch t {
	case 0x03, 0x04, 0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0xA1, 0xA2:
		return true
	}
	return false
}
