package internal

import (
	"encoding/json"
	"fmt"
	"os"
)

// Profile describes a single configured document profile: the display name
// used in responses/logs, and the password used to decrypt matching PDFs.
//
// The struct is intentionally small so additional metadata (e.g. an owner,
// a description, a retention policy) can be added later without touching
// matching or decryption logic.
type Profile struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// PatternConfig maps a case-insensitive matching substring to the profile
// that should be used when an identifier contains that substring.
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

	for pattern, profile := range cfg {
		if pattern == "" {
			return nil, fmt.Errorf("config contains an empty pattern key")
		}
		if profile.Name == "" {
			return nil, fmt.Errorf("pattern %q has an empty name", pattern)
		}
		if profile.Password == "" {
			return nil, fmt.Errorf("pattern %q has an empty password", pattern)
		}
	}

	return cfg, nil
}
