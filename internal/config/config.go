// Package config loads coachctl's settings: KEY=VALUE lines from
// ~/.config/coachctl/env (0600), overridden by the process environment.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata" // the binary carries its own zone database
)

// Keys read from the env file or the environment.
const (
	KeyHome           = "COACH_HOME"
	KeyTimezone       = "COACH_TIMEZONE"
	KeyIntervalsKey   = "INTERVALS_API_KEY"
	KeyIntervalsID    = "INTERVALS_ATHLETE_ID"
	KeyIntervalsBase  = "INTERVALS_API_BASE"
	KeyHevyKey        = "HEVY_API_KEY"
	KeyHevyBase       = "HEVY_API_BASE"
	KeyTelegramToken  = "TELEGRAM_BOT_TOKEN"
	KeyTelegramChat   = "TELEGRAM_CHAT_ID"
	KeyTelegramBase   = "TELEGRAM_API_BASE"
	KeyConfigOverride = "COACHCTL_CONFIG"
)

// Secret keys are never printed, only reported as set or unset.
var Secret = map[string]bool{KeyIntervalsKey: true, KeyHevyKey: true, KeyTelegramToken: true}

// Known lists every key coachctl reads, for doctor's unknown-key warning.
var Known = []string{
	KeyHome, KeyTimezone, KeyIntervalsKey, KeyIntervalsID, KeyIntervalsBase,
	KeyHevyKey, KeyHevyBase, KeyTelegramToken, KeyTelegramChat, KeyTelegramBase,
}

// Config is the merged result.
type Config struct {
	Path     string            // env file path (may not exist)
	FileMode fs.FileMode       // 0 when the file does not exist
	File     map[string]string // values from the file
	values   map[string]string // file values overridden by the environment
}

// DefaultPath is $COACHCTL_CONFIG, else $XDG_CONFIG_HOME/coachctl/env, else
// ~/.config/coachctl/env — on macOS too, so the documented path holds there.
func DefaultPath() (string, error) {
	if p := os.Getenv(KeyConfigOverride); p != "" {
		return p, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "coachctl", "env"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "coachctl", "env"), nil
}

// Load reads the env file (if any) and merges the environment over it.
func Load() (*Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	c := &Config{Path: path, File: map[string]string{}, values: map[string]string{}}
	if info, err := os.Stat(path); err == nil {
		c.FileMode = info.Mode().Perm()
		file, err := parseFile(path)
		if err != nil {
			return nil, err
		}
		c.File = file
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for k, v := range c.File {
		c.values[k] = v
	}
	for _, k := range Known {
		if v, ok := os.LookupEnv(k); ok {
			c.values[k] = v
		}
	}
	return c, nil
}

func parseFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		raw = strings.TrimPrefix(raw, "export ")
		k, v, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("%s line %d: expected KEY=VALUE", path, line)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out, sc.Err()
}

// Get returns a value, "" when unset.
func (c *Config) Get(key string) string { return c.values[key] }

// Has reports whether a key is set to a non-empty value.
func (c *Config) Has(key string) bool { return c.values[key] != "" }

// Home resolves COACH_HOME to an absolute path. A leading ~ is expanded.
func (c *Config) Home() (string, error) {
	home := c.Get(KeyHome)
	if home == "" {
		return "", fmt.Errorf("COACH_HOME is not set: add COACH_HOME=<workspace>/coaching to %s (see `coachctl setup`)", c.Path)
	}
	if strings.HasPrefix(home, "~/") {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, home[2:])
	}
	return filepath.Abs(home)
}

// Location is the coaching timezone: COACH_TIMEZONE, else the system zone.
func (c *Config) Location() (*time.Location, error) {
	name := c.Get(KeyTimezone)
	if name == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("COACH_TIMEZONE %q is not a known zone (use an IANA name like Europe/Berlin)", name)
	}
	return loc, nil
}
