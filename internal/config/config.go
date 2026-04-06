package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// AppConfig holds all application configuration values.
type AppConfig struct {
	Port                  int    `yaml:"port"`
	OutputDir             string `yaml:"output_dir"`
	AutoRip               bool   `yaml:"auto_rip"`
	MinTitleLength        int    `yaml:"min_title_length"`
	PollInterval          int    `yaml:"poll_interval"`
	GitHubClientID        string `yaml:"github_client_id"`
	GitHubClientSecret    string `yaml:"github_client_secret"`
	GitHubToken           string `yaml:"github_token"`
	DuplicateAction       string `yaml:"duplicate_action"`
	PreferredAudioLangs   string `yaml:"preferred_audio_langs"`    // comma-separated ISO 639-2 codes, e.g. "eng,jpn"
	PreferredSubtitleLangs string `yaml:"preferred_subtitle_langs"` // e.g. "eng"
	KeepForcedSubtitles   bool   `yaml:"keep_forced_subtitles"`    // default: true
	KeepLosslessAudio     bool   `yaml:"keep_lossless_audio"`      // default: true
	MakeMKVKey            string `yaml:"makemkv_key"`
	TMDBApiKey            string `yaml:"tmdb_api_key"`
}

// defaults returns an AppConfig populated with all default values.
func defaults() AppConfig {
	return AppConfig{
		Port:                9160,
		OutputDir:           "/output",
		AutoRip:             false,
		MinTitleLength:      120,
		PollInterval:        5,
		DuplicateAction:     "skip",
		KeepForcedSubtitles: true,
		KeepLosslessAudio:   true,
	}
}

// LoadFromEnv reads BLUFORGE_* environment variables and returns an AppConfig,
// starting from built-in defaults.
func LoadFromEnv() AppConfig {
	cfg := defaults()

	if v := os.Getenv("BLUFORGE_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Port = n
		} else {
			slog.Warn("ignoring invalid env var value", "var", "BLUFORGE_PORT", "value", v)
		}
	}
	if v := os.Getenv("BLUFORGE_OUTPUT_DIR"); v != "" {
		cfg.OutputDir = v
	}
	if v := os.Getenv("BLUFORGE_AUTO_RIP"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.AutoRip = b
		} else {
			slog.Warn("ignoring invalid env var value", "var", "BLUFORGE_AUTO_RIP", "value", v)
		}
	}
	if v := os.Getenv("BLUFORGE_MIN_TITLE_LENGTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MinTitleLength = n
		} else {
			slog.Warn("ignoring invalid env var value", "var", "BLUFORGE_MIN_TITLE_LENGTH", "value", v)
		}
	}
	if v := os.Getenv("BLUFORGE_POLL_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.PollInterval = n
		} else {
			slog.Warn("ignoring invalid env var value", "var", "BLUFORGE_POLL_INTERVAL", "value", v)
		}
	}
	if v := os.Getenv("BLUFORGE_GITHUB_CLIENT_ID"); v != "" {
		cfg.GitHubClientID = v
	}
	if v := os.Getenv("BLUFORGE_GITHUB_CLIENT_SECRET"); v != "" {
		cfg.GitHubClientSecret = v
	}
	if v := os.Getenv("BLUFORGE_GITHUB_TOKEN"); v != "" {
		cfg.GitHubToken = v
	}
	if v := os.Getenv("BLUFORGE_DUPLICATE_ACTION"); v != "" {
		cfg.DuplicateAction = v
	}
	if v := os.Getenv("BLUFORGE_PREFERRED_AUDIO_LANGS"); v != "" {
		cfg.PreferredAudioLangs = v
	}
	if v := os.Getenv("BLUFORGE_PREFERRED_SUBTITLE_LANGS"); v != "" {
		cfg.PreferredSubtitleLangs = v
	}
	if v := os.Getenv("BLUFORGE_KEEP_FORCED_SUBTITLES"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.KeepForcedSubtitles = b
		} else {
			slog.Warn("ignoring invalid env var value", "var", "BLUFORGE_KEEP_FORCED_SUBTITLES", "value", v)
		}
	}
	if v := os.Getenv("BLUFORGE_KEEP_LOSSLESS_AUDIO"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.KeepLosslessAudio = b
		} else {
			slog.Warn("ignoring invalid env var value", "var", "BLUFORGE_KEEP_LOSSLESS_AUDIO", "value", v)
		}
	}
	if v := os.Getenv("MAKEMKV_KEY"); v != "" {
		cfg.MakeMKVKey = v
	}
	if v := os.Getenv("BLUFORGE_TMDB_API_KEY"); v != "" {
		cfg.TMDBApiKey = v
	}

	return cfg
}

// Validate checks that the AppConfig fields hold valid values.
func (c *AppConfig) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("invalid port %d: must be 1-65535", c.Port)
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("invalid poll_interval %d: must be positive", c.PollInterval)
	}
	if c.MinTitleLength < 0 {
		return fmt.Errorf("invalid min_title_length %d: must be >= 0", c.MinTitleLength)
	}
	valid := map[string]bool{"skip": true, "overwrite": true, "rename": true}
	if _, ok := valid[c.DuplicateAction]; !ok {
		return fmt.Errorf("invalid duplicate_action %q: must be one of skip, overwrite, rename", c.DuplicateAction)
	}
	return nil
}

// Load reads env var defaults first, then overrides with the YAML config file
// at configPath (if it exists). The config file is the source of truth.
func Load(configPath string) (AppConfig, error) {
	cfg := LoadFromEnv()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file yet — env vars (and defaults) are sufficient.
			if err := cfg.Validate(); err != nil {
				return cfg, err
			}
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}

	if err := cfg.Validate(); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// Save writes cfg to a YAML file at configPath, creating or overwriting it.
func Save(cfg AppConfig, configPath string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0600)
}
