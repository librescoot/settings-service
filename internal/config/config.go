package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

const TomlFilePath = "/data/settings.toml"

type Config struct {
	Scooter   map[string]interface{} `toml:"scooter"`
	Cellular  map[string]interface{} `toml:"cellular"`
	Updates   map[string]interface{} `toml:"updates"`
	Dashboard map[string]interface{} `toml:"dashboard"`
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

	var config Config
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse TOML file: %w", err)
	}

	return &config, nil
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
	if err := encoder.Encode(config); err != nil {
		return fmt.Errorf("failed to encode TOML: %w", err)
	}

	return nil
}

// ParseRedisSettings converts Redis hash fields to Config structure
func ParseRedisSettings(settings map[string]string) *Config {
	config := &Config{
		Scooter:   make(map[string]interface{}),
		Cellular:  make(map[string]interface{}),
		Updates:   make(map[string]interface{}),
		Dashboard: make(map[string]interface{}),
	}

	for field, value := range settings {
		if len(field) > 8 && field[:8] == "scooter." {
			key := field[8:]
			config.Scooter[key] = value
		} else if len(field) > 9 && field[:9] == "cellular." {
			key := field[9:]
			config.Cellular[key] = value
		} else if len(field) > 8 && field[:8] == "updates." {
			key := field[8:]
			config.Updates[key] = value
		} else if len(field) > 10 && field[:10] == "dashboard." {
			key := field[10:]
			config.Dashboard[key] = value
		}
	}

	return config
}

// ToRedisFields converts Config to Redis hash fields
func (c *Config) ToRedisFields() map[string]interface{} {
	fields := make(map[string]interface{})

	for key, value := range c.Scooter {
		fields[fmt.Sprintf("scooter.%s", key)] = fmt.Sprintf("%v", value)
	}

	for key, value := range c.Cellular {
		fields[fmt.Sprintf("cellular.%s", key)] = fmt.Sprintf("%v", value)
	}

	for key, value := range c.Updates {
		fields[fmt.Sprintf("updates.%s", key)] = fmt.Sprintf("%v", value)
	}

	for key, value := range c.Dashboard {
		fields[fmt.Sprintf("dashboard.%s", key)] = fmt.Sprintf("%v", value)
	}

	return fields
}