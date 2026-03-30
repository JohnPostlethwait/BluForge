package config

import (
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// AppConfig holds all application configuration values.
type AppConfig struct {
	Port             int    `yaml:"port"`
	OutputDir        string `yaml:"output_dir"`
	AutoRip          bool   `yaml:"auto_rip"`
	MinTitleLength   int    `yaml:"min_title_length"`
	PollInterval     int    `yaml:"poll_interval"`
	MovieTemplate    string `yaml:"movie_template"`
	SeriesTemplate   string `yaml:"series_template"`
	GitHubClientID   string `yaml:"github_client_id"`
	GitHubClientSecret string `yaml:"github_client_secret"`
	DuplicateAction  string `yaml:"duplicate_action"`
}

// defaults returns an AppConfig populated with all default values.
func defaults() AppConfig {
	return AppConfig{
		Port:           9160,
		OutputDir:      "/output",
		AutoRip:        false,
		MinTitleLength: 120,
		PollInterval:   5,
		MovieTemplate:  "Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})",
		SeriesTemplate: "TV/{{.Show}}/Season {{.Season}}/{{.Show}} - S{{.Season}}E{{.Episode}} - {{.EpisodeTitle}}",
		DuplicateAction: "skip",
	}
}

// LoadFromEnv reads BLUFORGE_* environment variables and returns an AppConfig,
// starting from built-in defaults.
func LoadFromEnv() AppConfig {
	cfg := defaults()

	if v := os.Getenv("BLUFORGE_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Port = n
		}
	}
	if v := os.Getenv("BLUFORGE_OUTPUT_DIR"); v != "" {
		cfg.OutputDir = v
	}
	if v := os.Getenv("BLUFORGE_AUTO_RIP"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.AutoRip = b
		}
	}
	if v := os.Getenv("BLUFORGE_MIN_TITLE_LENGTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MinTitleLength = n
		}
	}
	if v := os.Getenv("BLUFORGE_POLL_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.PollInterval = n
		}
	}
	if v := os.Getenv("BLUFORGE_MOVIE_TEMPLATE"); v != "" {
		cfg.MovieTemplate = v
	}
	if v := os.Getenv("BLUFORGE_SERIES_TEMPLATE"); v != "" {
		cfg.SeriesTemplate = v
	}
	if v := os.Getenv("BLUFORGE_GITHUB_CLIENT_ID"); v != "" {
		cfg.GitHubClientID = v
	}
	if v := os.Getenv("BLUFORGE_GITHUB_CLIENT_SECRET"); v != "" {
		cfg.GitHubClientSecret = v
	}
	if v := os.Getenv("BLUFORGE_DUPLICATE_ACTION"); v != "" {
		cfg.DuplicateAction = v
	}

	return cfg
}

// Load reads env var defaults first, then overrides with the YAML config file
// at configPath (if it exists). The config file is the source of truth.
func Load(configPath string) (AppConfig, error) {
	cfg := LoadFromEnv()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file yet — env vars (and defaults) are sufficient.
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
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
