package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/librescoot/settings-service/internal/fileutil"
	"github.com/librescoot/settings-service/internal/schema"
)

var TomlFilePath = "/data/settings.toml"

// Config mirrors TOML tables directly. Setting namespaces come from the
// schema and may grow without changing this persistence layer.
type Config map[string]interface{}

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

func SaveToFile(config *Config) error {
	if err := os.MkdirAll(filepath.Dir(TomlFilePath), 0755); err != nil {
		return fmt.Errorf("failed to create settings directory: %w", err)
	}

	mode := os.FileMode(0644)
	if info, err := os.Lstat(TomlFilePath); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("settings path is not a regular file")
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect settings file: %w", err)
	}
	return fileutil.AtomicWrite(TomlFilePath, mode, func(f *os.File) error {
		return toml.NewEncoder(f).Encode(config)
	})
}

// Section returns a top-level TOML table, creating it when needed.
func (c Config) Section(namespace string) map[string]interface{} {
	section, ok := c[namespace].(map[string]interface{})
	if !ok {
		section = make(map[string]interface{})
		c[namespace] = section
	}
	return section
}

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

func ParseRedisSettings(settings map[string]string, sch *schema.Schema) *Config {
	config := make(Config)
	for field, value := range settings {
		segs := strings.Split(field, ".")
		if len(segs) < 2 || segs[0] == "" {
			continue
		}
		setNested(config.Section(segs[0]), segs[1:], tomlValue(sch, field, value))
	}
	return &config
}

// tomlValue converts a Redis string into the TOML-side value. Array-typed
// settings decode from their JSON wire form; everything else stays a string.
func tomlValue(sch *schema.Schema, field, value string) interface{} {
	if sch != nil && sch.Settings[field].Type == "array" {
		var array []interface{}
		if err := json.Unmarshal([]byte(value), &array); err == nil {
			return array
		}
	}
	return value
}

func flattenSection(prefix string, m map[string]interface{}, out map[string]interface{}) {
	for k, v := range m {
		key := prefix + "." + k
		if sub, ok := v.(map[string]interface{}); ok {
			flattenSection(key, sub, out)
		} else {
			out[key] = redisValue(v)
		}
	}
}

// redisValue renders a TOML-side value in its Redis string form. Arrays
// encode as compact JSON; everything else keeps the plain string form.
func redisValue(v interface{}) string {
	if array, ok := v.([]interface{}); ok {
		if data, err := json.Marshal(array); err == nil {
			return string(data)
		}
	}
	return fmt.Sprintf("%v", v)
}

func (c *Config) ToRedisFields() map[string]interface{} {
	fields := make(map[string]interface{})
	for namespace, rawSection := range *c {
		section, ok := rawSection.(map[string]interface{})
		if !ok {
			continue
		}
		flattenSection(namespace, section, fields)
	}
	return fields
}
