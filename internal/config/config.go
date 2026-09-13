// Package config is the settings file: where downloads go, what quality to ask for, how
// files are named, and the account token everything needs.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/davidetoniatti/qoget/internal/naming"
)

// Config is the settings file. Field names are the keys `qoget config set` accepts.
type Config struct {
	// AuthToken is the account's X-User-Auth-Token, taken from a logged-in browser session.
	// Qobuz stopped accepting email and password from third parties in April 2026, so this
	// is the only credential there is.
	AuthToken string `json:"auth_token"`

	// AppID and Secrets are what the web player publishes. Kept once read so that a run
	// does not fetch the player bundle every time; cleared when they stop working.
	AppID   string   `json:"app_id,omitempty"`
	Secrets []string `json:"secrets,omitempty"`

	Directory    string `json:"directory"`
	Quality      string `json:"quality"`
	Limit        int    `json:"limit"`
	FolderFormat string `json:"folder_format"`
	TrackFormat  string `json:"track_format"`
	EmbedArt     bool   `json:"embed_art"`
	NoCover      bool   `json:"no_cover"`
	CoverSize    string `json:"cover_size"`
	AlbumM3U     bool   `json:"album_m3u"`
	NoM3U        bool   `json:"no_playlist_m3u"`
	NoFallback   bool   `json:"no_fallback"`
	AlbumsOnly   bool   `json:"albums_only"`
	NoHistory    bool   `json:"no_history"`
}

// Default is the configuration a fresh install runs with.
func Default() *Config {
	return &Config{
		Directory:    filepath.Join(homeDir(), "Music", "Qobuz Downloads"),
		Quality:      "flac-24-192",
		Limit:        20,
		FolderFormat: naming.DefaultFolder,
		TrackFormat:  naming.DefaultTrack,
		CoverSize:    "max",
	}
}

// Path is where the settings file lives: $QOGET_CONFIG, else
// $XDG_CONFIG_HOME/qoget/config.json, else ~/.config/qoget/config.json.
func Path() string {
	if p := os.Getenv("QOGET_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(Dir(), "config.json")
}

// Dir is the directory the settings and the download history live in.
func Dir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "qoget")
	}
	return filepath.Join(homeDir(), ".config", "qoget")
}

// HistoryPath is where the list of downloaded ids lives.
func HistoryPath() string {
	return filepath.Join(Dir(), "downloaded.txt")
}

// Load reads the settings at path over the defaults, then applies the environment:
// QOGET_AUTH_TOKEN, QOGET_QUALITY and QOGET_DIRECTORY override the file. A missing
// file is the defaults.
func Load(path string) (*Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config: %s: %w", path, err)
		}
	}
	if v := os.Getenv("QOGET_AUTH_TOKEN"); v != "" {
		cfg.AuthToken = v
	}
	if v := os.Getenv("QOGET_QUALITY"); v != "" {
		cfg.Quality = v
	}
	if v := os.Getenv("QOGET_DIRECTORY"); v != "" {
		cfg.Directory = v
	}
	if cfg.FolderFormat == "" {
		cfg.FolderFormat = naming.DefaultFolder
	}
	if cfg.TrackFormat == "" {
		cfg.TrackFormat = naming.DefaultTrack
	}
	cfg.Directory = expandHome(cfg.Directory)
	return cfg, nil
}

// Save writes the settings, readable by the owner only since they hold the token.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// Keys lists what Set accepts, sorted.
func Keys() []string {
	keys := make([]string, 0, len(setters))
	for k := range setters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var setters = map[string]func(*Config, string) error{
	"auth_token":    func(c *Config, v string) error { c.AuthToken = v; return nil },
	"directory":     func(c *Config, v string) error { c.Directory = v; return nil },
	"quality":       func(c *Config, v string) error { c.Quality = v; return nil },
	"folder_format": func(c *Config, v string) error { c.FolderFormat = v; return nil },
	"track_format":  func(c *Config, v string) error { c.TrackFormat = v; return nil },
	"cover_size":    func(c *Config, v string) error { c.CoverSize = v; return nil },
	"limit": func(c *Config, v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return fmt.Errorf("limit must be a positive number, not %q", v)
		}
		c.Limit = n
		return nil
	},
	"embed_art":       boolSetter(func(c *Config, b bool) { c.EmbedArt = b }),
	"no_cover":        boolSetter(func(c *Config, b bool) { c.NoCover = b }),
	"album_m3u":       boolSetter(func(c *Config, b bool) { c.AlbumM3U = b }),
	"no_playlist_m3u": boolSetter(func(c *Config, b bool) { c.NoM3U = b }),
	"no_fallback":     boolSetter(func(c *Config, b bool) { c.NoFallback = b }),
	"albums_only":     boolSetter(func(c *Config, b bool) { c.AlbumsOnly = b }),
	"no_history":      boolSetter(func(c *Config, b bool) { c.NoHistory = b }),
}

func boolSetter(apply func(*Config, bool)) func(*Config, string) error {
	return func(c *Config, v string) error {
		b, err := strconv.ParseBool(strings.ToLower(v))
		if err != nil {
			return fmt.Errorf("expected true or false, not %q", v)
		}
		apply(c, b)
		return nil
	}
}

// Set changes one key from its textual value, as typed on the command line.
func (c *Config) Set(key, value string) error {
	setter, known := setters[strings.ToLower(key)]
	if !known {
		return fmt.Errorf("config: unknown key %q (one of %s)", key, strings.Join(Keys(), ", "))
	}
	return setter(c, value)
}

// Redacted is the configuration with the token and secrets hidden, for printing.
func (c *Config) Redacted() *Config {
	out := *c
	if out.AuthToken != "" {
		out.AuthToken = "<set, " + strconv.Itoa(len(c.AuthToken)) + " characters>"
	}
	if len(out.Secrets) > 0 {
		out.Secrets = []string{"<" + strconv.Itoa(len(c.Secrets)) + " secrets>"}
	}
	return &out
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(homeDir(), strings.TrimPrefix(p, "~"))
	}
	return p
}
