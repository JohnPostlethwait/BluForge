package organizer

import (
	"strings"
	"testing"
)

func TestSanitizeStripsInvalidChars(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Movie: The Sequel", "Movie - The Sequel"},
		{`He said "hello"`, "He said 'hello'"},
		{"What?", "What"},
		{"file<name>.mkv", "filename.mkv"},
		{`path/to\file`, "pathtofile"},
		{"pipe|here", "pipehere"},
		{"star*wars", "starwars"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeFilename(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSanitizeControlChars(t *testing.T) {
	input := "hello\x00world\x1Fend\x7F"
	expected := "helloworldend"
	got := SanitizeFilename(input)
	if got != expected {
		t.Errorf("SanitizeFilename(%q) = %q, want %q", input, got, expected)
	}
}

func TestSanitizeReservedWindowsNames(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"CON", "_CON"},
		{"PRN", "_PRN"},
		{"NUL", "_NUL"},
		{"COM1", "_COM1"},
		{"LPT9", "_LPT9"},
		{"con", "_con"},
		{"CONSOLE", "CONSOLE"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeFilename(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSanitizeWhitespace(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  hello  world  ", "hello world"},
		{"multiple   spaces", "multiple spaces"},
		{"\thello\t", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeFilename(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSanitizeMaxLength(t *testing.T) {
	input := strings.Repeat("a", 300)
	got := SanitizeFilename(input)
	if len(got) > 255 {
		t.Errorf("SanitizeFilename(300 'a's) returned %d bytes, want <= 255", len(got))
	}
	if len(got) != 255 {
		t.Errorf("SanitizeFilename(300 'a's) returned %d bytes, want exactly 255", len(got))
	}
}
