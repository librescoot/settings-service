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
	Scooter   map[string]interface{} `toml:"scooter"`
	Cellular  map[string]interface{} `toml:"cellular"`
	Updates   map[string]interface{} `toml:"updates"`
	Dashboard map[string]interface{} `toml:"dashboard"`
	Alarm     map[string]interface{} `toml:"alarm"`
	EngineECU map[string]interface{} `toml:"engine-ecu"`
	Keycard   map[string]interface{} `toml:"keycard"`
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

	return fileutil.AtomicWrite(TomlFilePath, 0644, func(f *os.File) error {
		return toml.NewEncoder(f).Encode(config)
	})
}

// ParseRedisSettings converts Redis hash fields to Config structure
func ParseRedisSettings(settings map[string]string) *Config {
	config := &Config{
		Scooter:   make(map[string]interface{}),
		Cellular:  make(map[string]interface{}),
		Updates:   make(map[string]interface{}),
		Dashboard: make(map[string]interface{}),
		Alarm:     make(map[string]interface{}),
		EngineECU: make(map[string]interface{}),
		Keycard:   make(map[string]interface{}),
	}

	for field, value := range settings {
		if strings.HasPrefix(field, "scooter.") {
			config.Scooter[strings.TrimPrefix(field, "scooter.")] = value
		} else if strings.HasPrefix(field, "cellular.") {
			config.Cellular[strings.TrimPrefix(field, "cellular.")] = value
		} else if strings.HasPrefix(field, "updates.") {
			config.Updates[strings.TrimPrefix(field, "updates.")] = value
		} else if strings.HasPrefix(field, "dashboard.") {
			config.Dashboard[strings.TrimPrefix(field, "dashboard.")] = value
		} else if strings.HasPrefix(field, "alarm.") {
			config.Alarm[strings.TrimPrefix(field, "alarm.")] = value
		} else if strings.HasPrefix(field, "engine-ecu.") {
			config.EngineECU[strings.TrimPrefix(field, "engine-ecu.")] = value
		} else if strings.HasPrefix(field, "keycard.") {
			config.Keycard[strings.TrimPrefix(field, "keycard.")] = value
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

	for key, value := range c.Alarm {
		fields[fmt.Sprintf("alarm.%s", key)] = fmt.Sprintf("%v", value)
	}

	for key, value := range c.EngineECU {
		fields[fmt.Sprintf("engine-ecu.%s", key)] = fmt.Sprintf("%v", value)
	}

	for key, value := range c.Keycard {
		fields[fmt.Sprintf("keycard.%s", key)] = fmt.Sprintf("%v", value)
	}

	return fields
}