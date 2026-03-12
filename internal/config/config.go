package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	Arr     ArrConfig     `yaml:"arr"`
}

type ArrConfig struct {
	Instances []ArrInstance `yaml:"instances"`
}

type ArrInstance struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`    // "sonarr" or "radarr"
	URL    string `yaml:"url"`
	APIKey string `yaml:"api_key"`
}

type XtreamConfig struct {
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type TMDBConfig struct {
	APIKey     string `yaml:"api_key"`
	TVDBAPIKey string `yaml:"tvdb_api_key"` // optional; enables TVDB series fallback
}

type OutputConfig struct {
	Path       string `yaml:"path"`
	MoviesDir  string `yaml:"movies_dir"`
	SeriesDir  string `yaml:"series_dir"`
}

type SyncConfig struct {
	Interval             string   `yaml:"interval"`
	OnStartup            bool     `yaml:"on_startup"`
	Parallelism          int      `yaml:"parallelism"`
	GraceCycles          int      `yaml:"grace_cycles"`
	TitleCleanupPatterns []string `yaml:"title_cleanup_patterns,omitempty"`

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

// Save marshals cfg to YAML and writes it atomically to path.
func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Explicitly restrict config file to owner-only before writing credentials
	if err := os.Chmod(tmpName, 0600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// CheckWritable verifies that path is (or can be created as) a writable directory
// by creating and immediately removing a probe file. Returns nil on success.
func CheckWritable(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("output path not writable: %w", err)
	}
	probe, err := os.CreateTemp(path, ".vodarr-write-test-*")
	if err != nil {
		return fmt.Errorf("output path not writable: %w", err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return nil
}

// Validate checks that all required config fields are set and valid.
func (c *Config) Validate() error {
	return c.validate()
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
			Interval:    "6h",
			OnStartup:   true,
			Parallelism: 10,
			GraceCycles: 3,
		},
		Server: ServerConfig{
			NewznabPort: 9091,
			QbitPort:    9092,
			WebPort:     9090,
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
	if !filepath.IsAbs(c.Output.Path) {
		return fmt.Errorf("output.path must be absolute")
	}
	cleaned := filepath.Clean(c.Output.Path)
	systemDirs := []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys", "/var/run"}
	for _, blocked := range systemDirs {
		if cleaned == blocked || strings.HasPrefix(cleaned, blocked+"/") {
			return fmt.Errorf("output.path must not be a system directory")
		}
	}

	d, err := time.ParseDuration(c.Sync.Interval)
	if err != nil {
		return fmt.Errorf("sync.interval is invalid: %w", err)
	}
	if d <= 0 {
		return fmt.Errorf("sync.interval must be positive")
	}
	c.Sync.ParsedInterval = d

	if c.Sync.Parallelism < 1 {
		c.Sync.Parallelism = 1
	}
	if c.Sync.Parallelism > 20 {
		c.Sync.Parallelism = 20
	}

	return nil
}
