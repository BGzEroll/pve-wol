package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration accepts the usual Go duration syntax in YAML, for example 60s or 5m.
// Numeric values are also accepted as nanoseconds for compatibility with YAML's
// normal time.Duration representation.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Tag == "!!str" {
		value, err := time.ParseDuration(strings.TrimSpace(node.Value))
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", node.Value, err)
		}
		*d = Duration(value)
		return nil
	}

	var nanos int64
	if err := node.Decode(&nanos); err != nil {
		return fmt.Errorf("duration must be a string such as 60s: %w", err)
	}
	*d = Duration(time.Duration(nanos))
	return nil
}

func (d Duration) Value() time.Duration {
	return time.Duration(d)
}

type File struct {
	PVE          PVEConfig `yaml:"pve"`
	WOL          WOLConfig `yaml:"wol"`
	SyncInterval Duration  `yaml:"sync_interval"`
}

type PVEConfig struct {
	URL                string `yaml:"url"`
	TokenID            string `yaml:"token_id"`
	TokenSecret        string `yaml:"token_secret"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

type WOLConfig struct {
	Listen   []string `yaml:"listen"`
	Debounce Duration `yaml:"debounce"`
}

func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg File
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return File{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.SyncInterval == 0 {
		cfg.SyncInterval = Duration(60 * time.Second)
	}
	if len(cfg.WOL.Listen) == 0 {
		cfg.WOL.Listen = []string{":7", ":9"}
	}
	if cfg.WOL.Debounce == 0 {
		cfg.WOL.Debounce = Duration(5 * time.Second)
	}

	if err := validate(cfg); err != nil {
		return File{}, err
	}
	return cfg, nil
}

func validate(cfg File) error {
	if strings.TrimSpace(cfg.PVE.URL) == "" {
		return errors.New("pve.url is required")
	}
	if strings.TrimSpace(cfg.PVE.TokenID) == "" {
		return errors.New("pve.token_id is required")
	}
	if strings.TrimSpace(cfg.PVE.TokenSecret) == "" || cfg.PVE.TokenSecret == "CHANGE_ME" {
		return errors.New("pve.token_secret must be configured")
	}
	if cfg.SyncInterval.Value() <= 0 {
		return errors.New("sync_interval must be greater than zero")
	}
	if cfg.WOL.Debounce.Value() < 0 {
		return errors.New("wol.debounce cannot be negative")
	}
	for _, address := range cfg.WOL.Listen {
		if strings.TrimSpace(address) == "" {
			return errors.New("wol.listen contains an empty address")
		}
	}
	return nil
}
