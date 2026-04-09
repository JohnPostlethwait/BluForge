package config

import (
	"strings"
	"testing"
)

// validConfig returns a config that passes Validate, used as a base for
// mutating individual fields.
func validConfig() AppConfig {
	return AppConfig{
		Port:            9160,
		OutputDir:       "/output",
		PollInterval:    5,
		MinTitleLength:  120,
		DuplicateAction: "skip",
	}
}

func TestValidate_PortBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"port 0 is invalid", 0, true},
		{"port 1 is valid", 1, false},
		{"port 65535 is valid", 65535, false},
		{"port 65536 is invalid", 65536, true},
		{"port -1 is invalid", -1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Port = tt.port
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with port %d: err = %v, wantErr = %v", tt.port, err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "port") {
				t.Errorf("expected error to mention 'port', got: %v", err)
			}
		})
	}
}

func TestValidate_DuplicateActionValues(t *testing.T) {
	tests := []struct {
		name    string
		action  string
		wantErr bool
	}{
		{"skip is valid", "skip", false},
		{"overwrite is valid", "overwrite", false},
		{"rename is valid", "rename", false},
		{"empty is invalid", "", true},
		{"unknown value is invalid", "replace", true},
		{"uppercase is invalid", "Skip", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.DuplicateAction = tt.action
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with duplicate_action %q: err = %v, wantErr = %v", tt.action, err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "duplicate_action") {
				t.Errorf("expected error to mention 'duplicate_action', got: %v", err)
			}
		})
	}
}

func TestValidate_PollIntervalBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		val     int
		wantErr bool
	}{
		{"zero is invalid", 0, true},
		{"negative is invalid", -1, true},
		{"one is valid", 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.PollInterval = tt.val
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with poll_interval %d: err = %v, wantErr = %v", tt.val, err, tt.wantErr)
			}
		})
	}
}

func TestValidate_MinTitleLengthBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		val     int
		wantErr bool
	}{
		{"zero is valid", 0, false},
		{"negative is invalid", -1, true},
		{"positive is valid", 120, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.MinTitleLength = tt.val
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() with min_title_length %d: err = %v, wantErr = %v", tt.val, err, tt.wantErr)
			}
		})
	}
}
