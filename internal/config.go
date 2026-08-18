package internal

import (
	"encoding/json"
	"fmt"
	"os"
)

// Profile describes a single configured document profile: the list of
// case-insensitive substrings that identify it, and the password used to
// decrypt matching PDFs.
//
// The struct is intentionally small so additional metadata (e.g. an owner,
// a description, a retention policy) can be added later without touching
// matching or decryption logic.
type Profile struct {
	Patterns []string `json:"patterns"`
	Password string   `json:"password"`
}

// PatternConfig maps a profile name to the profile definition (its matching
// patterns and password) that should be used when an identifier contains
// one of that profile's substrings.
type PatternConfig map[string]Profile

// LoadConfig reads and parses the pattern configuration file at path.
// The file is re-read on every call by design: configuration is expected to
// be mounted from a Kubernetes Secret volume that can change at runtime
// without requiring a pod restart.
func LoadConfig(path string) (PatternConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg PatternConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	if len(cfg) == 0 {
		return nil, fmt.Errorf("config file %q contains no patterns", path)
	}

	for name, profile := range cfg {
		if name == "" {
			return nil, fmt.Errorf("config contains an empty profile name")
		}
		if len(profile.Patterns) == 0 {
			return nil, fmt.Errorf("profile %q has no patterns", name)
		}
		for _, pattern := range profile.Patterns {
			if pattern == "" {
				return nil, fmt.Errorf("profile %q has an empty pattern", name)
			}
		}
		if profile.Password == "" {
			return nil, fmt.Errorf("profile %q has an empty password", name)
		}
	}

	return cfg, nil
}
