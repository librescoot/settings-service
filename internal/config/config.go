package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

const TomlFilePath = "/data/settings.toml"

type Config struct {
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

	var sections map[string]map[string]interface{}
	if err := toml.Unmarshal(data, &sections); err != nil {
		return nil, fmt.Errorf("failed to parse TOML file: %w", err)
	}

	return &Config{Sections: sections}, nil
}

// SaveToFile writes the configuration to the TOML file
func SaveToFile(config *Config) error {
	if err := os.MkdirAll("/data", 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	file, err := os.Create(TomlFilePath)
	if err != nil {
		return fmt.Errorf("failed to create TOML file: %w", err)
	}
	defer file.Close()

	encoder := toml.NewEncoder(file)
	if err := encoder.Encode(config.Sections); err != nil {
		return fmt.Errorf("failed to encode TOML: %w", err)
	}

	return nil
}

// ParseRedisSettings converts Redis hash fields to Config structure
func ParseRedisSettings(settings map[string]string) *Config {
	sections := make(map[string]map[string]interface{})

	for field, value := range settings {
		// Split on first dot to get section and key
		parts := strings.SplitN(field, ".", 2)
		if len(parts) != 2 {
			// Skip keys without dots
			continue
		}

		section := parts[0]
		key := parts[1]

		// Create section map if it doesn't exist
		if sections[section] == nil {
			sections[section] = make(map[string]interface{})
		}

		sections[section][key] = value
	}

	return &Config{Sections: sections}
}

// ToRedisFields converts Config to Redis hash fields
func (c *Config) ToRedisFields() map[string]interface{} {
	fields := make(map[string]interface{})

	for section, sectionMap := range c.Sections {
		for key, value := range sectionMap {
			fields[fmt.Sprintf("%s.%s", section, key)] = fmt.Sprintf("%v", value)
		}
	}

	return fields
}