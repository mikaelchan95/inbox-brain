// Package config manages the local Inbox Brain home directory
// (~/.inbox-brain by default, overridable with IB_HOME) and the
// configuration file holding the business profile and runtime options.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mikaelchan/inbox-brain/internal/model"
)

// Config is the persisted local configuration (config.json in the home dir).
type Config struct {
	home string

	Profile model.BusinessProfile `json:"profile"`

	// AIProvider selects the extraction provider: "anthropic" or "rules".
	AIProvider     string `json:"aiProvider"`
	AnthropicModel string `json:"anthropicModel"`

	// AutoMode processes conversations scoring >= AutoThreshold without review.
	// Off by default (spec §7.3).
	AutoMode      bool    `json:"autoMode"`
	AutoThreshold float64 `json:"autoThreshold"`

	// Port for the local dashboard/API server (ib dev).
	Port int `json:"port"`

	// SearchIncludeIgnored includes personal/ignored chats in search.
	// Off by default (spec §19).
	SearchIncludeIgnored bool `json:"searchIncludeIgnored"`
}

// Home returns the Inbox Brain home directory: $IB_HOME or ~/.inbox-brain.
func Home() string {
	if h := os.Getenv("IB_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".inbox-brain"
	}
	return filepath.Join(home, ".inbox-brain")
}

// DBPath returns the SQLite database path inside a home directory.
func DBPath(home string) string {
	return filepath.Join(home, "inbox.db")
}

func configPath(home string) string {
	return filepath.Join(home, "config.json")
}

// Default returns a fresh config with sensible defaults.
func Default(home string) *Config {
	return &Config{
		home: home,
		Profile: model.BusinessProfile{
			BusinessName:     "My Business",
			BusinessType:     "freelancer",
			Services:         []string{},
			BusinessKeywords: []string{},
			Timezone:         "Asia/Singapore",
			Tone:             "friendly",
			ReplyLanguage:    "English",
		},
		AIProvider:     "rules",
		AnthropicModel: "claude-haiku-4-5",
		AutoMode:       false,
		AutoThreshold:  model.ThresholdAuto,
		Port:           4173,
	}
}

// ErrNotInitialized is returned by Load when ib init has not been run.
var ErrNotInitialized = errors.New("inbox brain is not initialized; run: ib init")

// Init creates the home directory and a default config file if missing.
// It is safe to call repeatedly; an existing config is loaded, not replaced.
func Init(home string) (*Config, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory %s: %w", home, err)
	}
	if _, err := os.Stat(configPath(home)); err == nil {
		return Load(home)
	}
	cfg := Default(home)
	if err := cfg.Save(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Load reads config.json from the home directory.
func Load(home string) (*Config, error) {
	data, err := os.ReadFile(configPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotInitialized
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := Default(home)
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", configPath(home), err)
	}
	cfg.home = home
	return cfg, nil
}

// Save writes the config back to config.json (0600 — it may name private chats).
func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(c.home), append(data, '\n'), 0o600)
}

// HomeDir returns the home directory this config was loaded from.
func (c *Config) HomeDir() string { return c.home }
