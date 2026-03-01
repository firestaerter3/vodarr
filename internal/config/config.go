package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Xtream  XtreamConfig  `yaml:"xtream"`
	TMDB    TMDBConfig    `yaml:"tmdb"`
	Output  OutputConfig  `yaml:"output"`
	Sync    SyncConfig    `yaml:"sync"`
	Server  ServerConfig  `yaml:"server"`
	Logging LoggingConfig `yaml:"logging"`
}

type XtreamConfig struct {
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type TMDBConfig struct {
	APIKey string `yaml:"api_key"`
}

type OutputConfig struct {
	Path       string `yaml:"path"`
	MoviesDir  string `yaml:"movies_dir"`
	SeriesDir  string `yaml:"series_dir"`
}

type SyncConfig struct {
	Interval  string `yaml:"interval"`
	OnStartup bool   `yaml:"on_startup"`

	// Parsed interval (not from YAML directly)
	ParsedInterval time.Duration `yaml:"-"`
}

type ServerConfig struct {
	NewznabPort int    `yaml:"newznab_port"`
	QbitPort    int    `yaml:"qbit_port"`
	WebPort     int    `yaml:"web_port"`
	ExternalURL string `yaml:"external_url"` // 1B: e.g. "http://vodarr:7878" — overrides localhost fallback
	APIKey      string `yaml:"api_key"`      // 2C: Newznab API key; empty = no auth
	QbitUsername string `yaml:"qbit_username"` // 2D: qBit web UI credentials
	QbitPassword string `yaml:"qbit_password"`
	WebUsername string `yaml:"web_username"` // 2E: web UI basic auth
	WebPassword string `yaml:"web_password"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := defaults()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Output: OutputConfig{
			MoviesDir: "movies",
			SeriesDir: "tv",
		},
		Sync: SyncConfig{
			Interval:  "6h",
			OnStartup: true,
		},
		Server: ServerConfig{
			NewznabPort: 7878,
			QbitPort:    8080,
			WebPort:     3000,
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}

func (c *Config) validate() error {
	if c.Xtream.URL == "" {
		return fmt.Errorf("xtream.url is required")
	}
	if c.Xtream.Username == "" {
		return fmt.Errorf("xtream.username is required")
	}
	if c.Xtream.Password == "" {
		return fmt.Errorf("xtream.password is required")
	}
	if c.TMDB.APIKey == "" {
		return fmt.Errorf("tmdb.api_key is required")
	}
	if c.Output.Path == "" {
		return fmt.Errorf("output.path is required")
	}

	d, err := time.ParseDuration(c.Sync.Interval)
	if err != nil {
		return fmt.Errorf("sync.interval is invalid: %w", err)
	}
	if d <= 0 {
		return fmt.Errorf("sync.interval must be positive")
	}
	c.Sync.ParsedInterval = d

	return nil
}
