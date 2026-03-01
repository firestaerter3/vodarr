package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveRoundTrip(t *testing.T) {
	cfg := &Config{
		Xtream: XtreamConfig{
			URL:      "http://example.com",
			Username: "user",
			Password: "pass",
		},
		TMDB: TMDBConfig{APIKey: "abc123"},
		Output: OutputConfig{
			Path:      "/data/strm",
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
		Logging: LoggingConfig{Level: "info"},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Xtream.URL != cfg.Xtream.URL {
		t.Errorf("URL = %q, want %q", loaded.Xtream.URL, cfg.Xtream.URL)
	}
	if loaded.TMDB.APIKey != cfg.TMDB.APIKey {
		t.Errorf("APIKey = %q, want %q", loaded.TMDB.APIKey, cfg.TMDB.APIKey)
	}
	if loaded.Output.Path != cfg.Output.Path {
		t.Errorf("Output.Path = %q, want %q", loaded.Output.Path, cfg.Output.Path)
	}
	if loaded.Server.NewznabPort != cfg.Server.NewznabPort {
		t.Errorf("NewznabPort = %d, want %d", loaded.Server.NewznabPort, cfg.Server.NewznabPort)
	}
}

func TestSaveAtomicWrite(t *testing.T) {
	// Verifies no temp file is left behind after a successful save.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	cfg := minimalConfig()

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 file in dir, got %d: %v", len(entries), entries)
	}
	if entries[0].Name() != "config.yml" {
		t.Errorf("unexpected file: %s", entries[0].Name())
	}
}

func TestValidateRequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:    "missing xtream url",
			mutate:  func(c *Config) { c.Xtream.URL = "" },
			wantErr: "xtream.url is required",
		},
		{
			name:    "missing xtream username",
			mutate:  func(c *Config) { c.Xtream.Username = "" },
			wantErr: "xtream.username is required",
		},
		{
			name:    "missing xtream password",
			mutate:  func(c *Config) { c.Xtream.Password = "" },
			wantErr: "xtream.password is required",
		},
		{
			name:    "missing tmdb api_key",
			mutate:  func(c *Config) { c.TMDB.APIKey = "" },
			wantErr: "tmdb.api_key is required",
		},
		{
			name:    "missing output path",
			mutate:  func(c *Config) { c.Output.Path = "" },
			wantErr: "output.path is required",
		},
		{
			name:    "invalid interval",
			mutate:  func(c *Config) { c.Sync.Interval = "bogus" },
			wantErr: "sync.interval is invalid",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalConfig()
			tc.mutate(cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if tc.wantErr != "" {
				if !contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tc.wantErr)
				}
			}
		})
	}
}

func TestValidateOK(t *testing.T) {
	cfg := minimalConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func minimalConfig() *Config {
	return &Config{
		Xtream: XtreamConfig{
			URL:      "http://example.com",
			Username: "user",
			Password: "pass",
		},
		TMDB:   TMDBConfig{APIKey: "key"},
		Output: OutputConfig{Path: "/data/strm"},
		Sync:   SyncConfig{Interval: "6h"},
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
