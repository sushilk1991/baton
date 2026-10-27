package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths contains baton-owned locations, resolved without creating anything.
type Paths struct {
	// ConfigFile holds the optional TOML file on every OS.
	ConfigFile string
	// StateDir holds local locks, tickets, jobs, and history.
	StateDir string
	// CacheDir holds regenerable cached outputs.
	CacheDir string
	// DataDir holds installed shims and integration manifests.
	DataDir string
}

// ResolvePaths uses XDG overrides and Unix-style home defaults on every OS.
func ResolvePaths() (Paths, error) {
	var paths Paths
	for _, entry := range []struct {
		key      string
		fallback string
		target   *string
	}{
		{"XDG_CONFIG_HOME", ".config", &paths.ConfigFile},
		{"XDG_STATE_HOME", ".local/state", &paths.StateDir},
		{"XDG_CACHE_HOME", ".cache", &paths.CacheDir},
		{"XDG_DATA_HOME", ".local/share", &paths.DataDir},
	} {
		base := os.Getenv(entry.key)
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return Paths{}, err
			}
			base = filepath.Join(home, filepath.FromSlash(entry.fallback))
		}
		if !filepath.IsAbs(base) {
			return Paths{}, fmt.Errorf("%s must be absolute", entry.key)
		}
		*entry.target = filepath.Join(base, "baton")
	}
	paths.ConfigFile = filepath.Join(paths.ConfigFile, "config.toml")
	return paths, nil
}
