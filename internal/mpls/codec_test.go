package mpls

import "testing"

func TestCodingTypeToCodecID(t *testing.T) {
	tests := []struct {
		name string
		ct   byte
		want string
	}{
		// Audio types
		{"MPEG/L1", 0x03, "A_MPEG/L1"},
		{"MPEG/L2", 0x04, "A_MPEG/L2"},
		{"LPCM", 0x80, "A_LPCM"},
		{"AC3", 0x81, "A_AC3"},
		{"DTS", 0x82, "A_DTS"},
		{"TrueHD", 0x83, "A_TRUEHD"},
		{"EAC3", 0x84, "A_EAC3"},
		{"DTSHD_0x85", 0x85, "A_DTSHD"},
		{"DTSHD_0x86", 0x86, "A_DTSHD"},
		{"EAC3_secondary", 0xA1, "A_EAC3"},
		{"DTSHD_secondary", 0xA2, "A_DTSHD"},
		// Subtitle types
		{"PGS", 0x90, "S_HDMV/PGS"},
		{"IGS", 0x91, "S_HDMV/IGS"},
		{"TextST", 0x92, "S_TEXT/UTF8"},
		// Unknown types
		{"unknown_0x00", 0x00, ""},
		{"unknown_0xFF", 0xFF, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CodingTypeToCodecID(tt.ct)
			if got != tt.want {
				t.Errorf("CodingTypeToCodecID(0x%02X) = %q, want %q", tt.ct, got, tt.want)
			}
		})
	}
}

func TestCodingTypeToCodecShort(t *testing.T) {
	tests := []struct {
		name string
		ct   byte
		want string
	}{
		// Audio types
		{"LPCM", 0x80, "PCM"},
		{"AC3", 0x81, "AC3"},
		{"DTS", 0x82, "DTS"},
		{"TrueHD", 0x83, "TrueHD"},
		{"EAC3", 0x84, "E-AC3"},
		{"DTSHD_MA_0x85", 0x85, "DTS-HD MA"},
		{"DTSHD_MA_0x86", 0x86, "DTS-HD MA"},
		{"EAC3_secondary", 0xA1, "E-AC3"},
		{"DTSHD_secondary", 0xA2, "DTS-HD"},
		// Subtitle types
		{"PGS", 0x90, "PGS"},
		{"IGS", 0x91, "IGS"},
		{"SRT", 0x92, "SRT"},
		// Unknown types
		{"unknown_0x00", 0x00, ""},
		{"unknown_0xFF", 0xFF, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CodingTypeToCodecShort(tt.ct)
			if got != tt.want {
				t.Errorf("CodingTypeToCodecShort(0x%02X) = %q, want %q", tt.ct, got, tt.want)
			}
		})
	}
}
