package makemkv

import "testing"

func TestIsLosslessAudio(t *testing.T) {
	tests := []struct {
		codecShort string
		want       bool
	}{
		// Lossless codecs
		{"TrueHD", true},
		{"DTS-HD MA", true},
		{"FLAC", true},
		{"PCM", true},
		// Lossy codecs
		{"AC3", false},
		{"DTS", false},
		{"E-AC3", false},
		// Edge cases
		{"", false},
		{"truehd", false}, // case-sensitive: MakeMKV uses specific casing
	}

	for _, tt := range tests {
		name := tt.codecShort
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			got := IsLosslessAudio(tt.codecShort)
			if got != tt.want {
				t.Errorf("IsLosslessAudio(%q) = %v, want %v", tt.codecShort, got, tt.want)
			}
		})
	}
}
