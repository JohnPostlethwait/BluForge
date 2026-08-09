package makemkv

import (
	"errors"
	"fmt"
	"testing"
)

func TestSourceArg(t *testing.T) {
	tests := []struct {
		name string
		src  Source
		want string
	}{
		{"disc zero", DiscSource(0), "disc:0"},
		{"disc nonzero", DiscSource(3), "disc:3"},
		{"file source", FileSource("/output/.bluforge-scratch/slug"), "file:/output/.bluforge-scratch/slug"},
		{"file source with spaces", FileSource("/mnt/My Disc"), "file:/mnt/My Disc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.src.Arg(); got != tt.want {
				t.Errorf("Arg() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSourceIsDisc(t *testing.T) {
	if !DiscSource(0).IsDisc() {
		t.Error("DiscSource(0).IsDisc() = false, want true")
	}
	if FileSource("/tmp/x").IsDisc() {
		t.Error("FileSource.IsDisc() = true, want false")
	}
}

// TestIsSpuriousAACSSignature covers the exact failure fingerprint of a disc
// that carries an AACS directory over unencrypted content. All three of
// MSG:3303, MSG:5010 and a zero title count are required — a partial match
// must not trigger the (expensive, destructive-adjacent) recovery path.
func TestIsSpuriousAACSSignature(t *testing.T) {
	msg := func(codes ...int) []Message {
		out := make([]Message, 0, len(codes))
		for _, c := range codes {
			out = append(out, Message{Code: c, Text: "irrelevant localized text"})
		}
		return out
	}

	tests := []struct {
		name       string
		messages   []Message
		titleCount int
		want       bool
	}{
		{"full signature", msg(3303, 5010), 0, true},
		{"full signature with 5042 noise", msg(5042, 3303, 5010), 0, true},
		{"full signature among unrelated messages", msg(1005, 3303, 2003, 5010, 5085), 0, true},
		{"missing 3303", msg(5010), 0, false},
		{"missing 5010", msg(3303), 0, false},
		{"no messages", nil, 0, false},
		{"codes present but titles found", msg(3303, 5010), 4, false},
		{"only 5042", msg(5042), 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSpuriousAACSSignature(tt.messages, tt.titleCount); got != tt.want {
				t.Errorf("IsSpuriousAACSSignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasNoDrivesMessage(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		want     bool
	}{
		{"5042 present", []Message{{Code: 5042}}, true},
		{"5042 among others", []Message{{Code: 1005}, {Code: 5042}, {Code: 3303}}, true},
		{"absent", []Message{{Code: 1005}, {Code: 5010}}, false},
		{"empty", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasNoDrivesMessage(tt.messages); got != tt.want {
				t.Errorf("HasNoDrivesMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestScanErrorCarriesScan verifies that a failed scan surfaces the messages
// makemkvcon produced. Without this the caller cannot tell a spurious-AACS
// failure from any other "failed to open disc".
func TestScanErrorCarriesScan(t *testing.T) {
	scan := &DiscScan{
		DriveIndex: 1,
		Messages:   []Message{{Code: 3303}, {Code: 5010}},
	}
	var err error = &ScanError{Scan: scan, Reason: "Failed to open disc"}

	if err.Error() == "" {
		t.Error("ScanError.Error() returned empty string")
	}

	// Must survive wrapping — callers see it through fmt.Errorf chains.
	wrapped := fmt.Errorf("orchestrator: %w", err)

	var se *ScanError
	if !errors.As(wrapped, &se) {
		t.Fatal("errors.As failed to unwrap ScanError")
	}
	if !IsSpuriousAACSSignature(se.Scan.Messages, len(se.Scan.Titles)) {
		t.Error("signature not detectable through ScanError")
	}
}
