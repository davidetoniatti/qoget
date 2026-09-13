package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/davidetoniatti/qoget/internal/config"
	"github.com/davidetoniatti/qoget/internal/naming"
)

func TestLoadMissingFileIsTheDefaults(t *testing.T) {
	t.Setenv("QOGET_AUTH_TOKEN", "")
	t.Setenv("QOGET_QUALITY", "")
	t.Setenv("QOGET_DIRECTORY", "")
	cfg, err := config.Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Quality != "flac-24-192" || cfg.FolderFormat != naming.DefaultFolder || cfg.Limit != 20 {
		t.Errorf("defaults = %+v", cfg)
	}
}

func TestSaveLoadAndEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "config.json")
	cfg := config.Default()
	if err := cfg.Set("auth_token", "tok"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("embed_art", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("limit", "5"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("directory", "~/dl"); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no POSIX modes to check; everywhere else the file holds the token.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600: the file holds the token", info.Mode().Perm())
	}

	t.Setenv("QOGET_AUTH_TOKEN", "")
	t.Setenv("QOGET_QUALITY", "mp3-320")
	t.Setenv("QOGET_DIRECTORY", "")
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AuthToken != "tok" || !loaded.EmbedArt || loaded.Limit != 5 {
		t.Errorf("loaded = %+v", loaded)
	}
	if loaded.Quality != "mp3-320" {
		t.Errorf("quality = %q, want the environment to win", loaded.Quality)
	}
	home, _ := os.UserHomeDir()
	if loaded.Directory != filepath.Join(home, "dl") {
		t.Errorf("directory = %q, want ~ expanded", loaded.Directory)
	}
	if red := loaded.Redacted(); red.AuthToken == "tok" || !strings.Contains(red.AuthToken, "set") {
		t.Errorf("Redacted still shows the token: %q", red.AuthToken)
	}
}

func TestSetRefusesBadValues(t *testing.T) {
	cfg := config.Default()
	for key, value := range map[string]string{"limit": "zero", "embed_art": "maybe", "nonsense": "x"} {
		if err := cfg.Set(key, value); err == nil {
			t.Errorf("Set(%q, %q) should have failed", key, value)
		}
	}
}
