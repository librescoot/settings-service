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

type Config struct {
	Scooter   map[string]interface{} `toml:"scooter"`
	Cellular  map[string]interface{} `toml:"cellular"`
	Updates   map[string]interface{} `toml:"updates"`
	Dashboard map[string]interface{} `toml:"dashboard"`
	Alarm     map[string]interface{} `toml:"alarm"`
	EngineECU map[string]interface{} `toml:"engine-ecu"`
	Keycard   map[string]interface{} `toml:"keycard"`
	PM        map[string]interface{} `toml:"pm"`
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
	if err := os.MkdirAll(filepath.Dir(TomlFilePath), 0755); err != nil {
		return fmt.Errorf("failed to create settings directory: %w", err)
	}

	return fileutil.AtomicWrite(TomlFilePath, 0644, func(f *os.File) error {
		return toml.NewEncoder(f).Encode(config)
	})
}

// setNested walks segments, creating sub-maps as needed, and stores value at the leaf.
func setNested(m map[string]interface{}, segments []string, value interface{}) {
	for i, seg := range segments {
		if i == len(segments)-1 {
			m[seg] = value
			return
		}
		sub, ok := m[seg].(map[string]interface{})
		if !ok {
			sub = make(map[string]interface{})
			m[seg] = sub
		}
		m = sub
	}
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
		PM:        make(map[string]interface{}),
	}

	for field, value := range settings {
		segs := strings.Split(field, ".")
		if len(segs) < 2 {
			continue
		}
		var section map[string]interface{}
		switch segs[0] {
		case "scooter":
			section = config.Scooter
		case "cellular":
			section = config.Cellular
		case "updates":
			section = config.Updates
		case "dashboard":
			section = config.Dashboard
		case "alarm":
			section = config.Alarm
		case "engine-ecu":
			section = config.EngineECU
		case "keycard":
			section = config.Keycard
		case "pm":
			section = config.PM
		default:
			continue
		}
		setNested(section, segs[1:], value)
	}

	return config
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
func (c *Config) ToRedisFields() map[string]interface{} {
	fields := make(map[string]interface{})

	sections := map[string]map[string]interface{}{
		"scooter":    c.Scooter,
		"cellular":   c.Cellular,
		"updates":    c.Updates,
		"dashboard":  c.Dashboard,
		"alarm":      c.Alarm,
		"engine-ecu": c.EngineECU,
		"keycard":    c.Keycard,
		"pm":         c.PM,
	}
	for prefix, section := range sections {
		flattenSection(prefix, section, fields)
	}

	return fields
}
