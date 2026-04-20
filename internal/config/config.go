package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/librescoot/settings-service/internal/fileutil"
)

var TomlFilePath = "/data/settings.toml"

// Config is a generic two-level map: section name -> field name -> value.
// Values can be strings (for flat leaves) or nested map[string]interface{}
// (for sub-tables like [dashboard.saved-locations.0]).
type Config map[string]map[string]interface{}

// LoadFromFile reads the TOML configuration file
func LoadFromFile() (Config, error) {
	if _, err := os.Stat(TomlFilePath); os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}

	data, err := os.ReadFile(TomlFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read TOML file: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse TOML file: %w", err)
	}

	return cfg, nil
}

// SaveToFile writes the configuration to the TOML file
func SaveToFile(cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(TomlFilePath), 0755); err != nil {
		return fmt.Errorf("failed to create settings directory: %w", err)
	}

	return fileutil.AtomicWrite(TomlFilePath, 0644, func(f *os.File) error {
		return toml.NewEncoder(f).Encode(cfg)
	})
}

// ParseRedisSettings converts Redis hash fields to Config structure.
// Splits field names at the first dot: everything before becomes the top-level
// section, everything after is kept verbatim as the section's flat key so the
// TOML encoder emits existing-style quoted dotted keys.
func ParseRedisSettings(settings map[string]string) Config {
	cfg := Config{}
	for field, value := range settings {
		dot := strings.IndexByte(field, '.')
		if dot < 1 {
			continue
		}
		section := field[:dot]
		key := field[dot+1:]
		if _, ok := cfg[section]; !ok {
			cfg[section] = map[string]interface{}{}
		}
		cfg[section][key] = value
	}
	return cfg
}

// flattenSection walks a section map, handling both flat string leaves and
// nested sub-maps (produced by TOML files that use sub-tables like
// [dashboard.saved-locations.0]). Emits dotted Redis field names at the leaves.
func flattenSection(prefix string, m map[string]interface{}, out map[string]interface{}) {
	for k, v := range m {
		key := prefix + "." + k
		if sub, ok := v.(map[string]interface{}); ok {
			flattenSection(key, sub, out)
		} else {
			out[key] = fmt.Sprintf("%v", v)
		}
	}
}

// ToRedisFields converts Config to Redis hash fields
func (c Config) ToRedisFields() map[string]interface{} {
	fields := make(map[string]interface{})
	for prefix, section := range c {
		flattenSection(prefix, section, fields)
	}
	return fields
}
