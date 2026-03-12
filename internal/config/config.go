package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/librescoot/settings-service/internal/fileutil"
)

const TomlFilePath = "/data/settings.toml"

type Config struct {
	Fields   map[string]interface{}
	Sections map[string]map[string]interface{}
}

// LoadFromFile reads the TOML configuration file
func LoadFromFile() (*Config, error) {
	if _, err := os.Stat(TomlFilePath); os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}

	data, err := os.ReadFile(TomlFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read TOML file: %w", err)
	}

	var raw map[string]interface{}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse TOML file: %w", err)
	}

	cfg := &Config{
		Fields:   make(map[string]interface{}),
		Sections: make(map[string]map[string]interface{}),
	}
	for key, val := range raw {
		if section, ok := val.(map[string]interface{}); ok {
			cfg.Sections[key] = section
		} else {
			cfg.Fields[key] = val
		}
	}

	return cfg, nil
}

// SaveToFile writes the configuration to the TOML file
func SaveToFile(config *Config) error {
	if err := os.MkdirAll("/data", 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	return fileutil.AtomicWrite(TomlFilePath, 0644, func(f *os.File) error {
		out := make(map[string]interface{}, len(config.Fields)+len(config.Sections))
		for k, v := range config.Fields {
			out[k] = v
		}
		for k, v := range config.Sections {
			out[k] = v
		}
		return toml.NewEncoder(f).Encode(out)
	})
}

// ParseRedisSettings converts Redis hash fields to Config structure
func ParseRedisSettings(settings map[string]string) *Config {
	cfg := &Config{
		Fields:   make(map[string]interface{}),
		Sections: make(map[string]map[string]interface{}),
	}

	for field, value := range settings {
		parts := strings.SplitN(field, ".", 2)
		if len(parts) != 2 {
			cfg.Fields[field] = value
			continue
		}

		section := parts[0]
		key := parts[1]

		if cfg.Sections[section] == nil {
			cfg.Sections[section] = make(map[string]interface{})
		}

		cfg.Sections[section][key] = value
	}

	return cfg
}

// ToRedisFields converts Config to Redis hash fields
func (c *Config) ToRedisFields() map[string]interface{} {
	fields := make(map[string]interface{})

	for key, value := range c.Fields {
		fields[key] = fmt.Sprintf("%v", value)
	}

	for section, sectionMap := range c.Sections {
		for key, value := range sectionMap {
			fields[fmt.Sprintf("%s.%s", section, key)] = fmt.Sprintf("%v", value)
		}
	}

	return fields
}
