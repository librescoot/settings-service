package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type EnumValue struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type Setting struct {
	Type            string          `json:"type"`
	Description     string          `json:"description"`
	Label           string          `json:"label,omitempty"`
	UserVisible     bool            `json:"user-visible,omitempty"`
	Service         string          `json:"service,omitempty"`
	Default         any             `json:"default,omitempty"`
	Values          []EnumValue     `json:"values,omitempty"`
	Unit            string          `json:"unit,omitempty"`
	Min             *float64        `json:"min,omitempty"`
	Max             *float64        `json:"max,omitempty"`
	Example         any             `json:"example,omitempty"`
	ReadOnly        bool            `json:"read-only,omitempty"`
	Pattern         string          `json:"pattern,omitempty"`
	Format          string          `json:"format,omitempty"`
	Transient       bool            `json:"transient,omitempty"`
	ChannelDefaults map[string]bool `json:"channel-defaults,omitempty"`
}

type Schema struct {
	Settings map[string]Setting
	Raw      []byte
}

func Parse(data []byte) (*Schema, error) {
	var settings map[string]Setting
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parsing schema: %w", err)
	}
	return &Schema{
		Settings: settings,
		Raw:      data,
	}, nil
}

func LoadFile(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading schema file: %w", err)
	}
	return Parse(data)
}

// IsTransient reports settings that are never read from or written to TOML.
// Defaults still hydrate into Redis each boot; runtime overrides do not persist.
func (s *Schema) IsTransient(key string) bool {
	if s == nil {
		return false
	}
	setting, ok := s.Settings[key]
	return ok && setting.Transient
}

// ValidateValue applies the setting's explicit format and enum constraints.
func (s *Schema) ValidateValue(key, value string) error {
	if s == nil {
		return nil
	}
	setting, ok := s.Settings[key]
	if !ok {
		return nil
	}
	if err := ValidateFormat(setting.Format, value); err != nil {
		return err
	}
	if len(setting.Values) == 0 {
		return nil
	}
	for _, candidate := range setting.Values {
		if value == candidate.Value {
			return nil
		}
	}
	return fmt.Errorf("value %q is not allowed for %s", value, key)
}

func (s *Schema) HasValidation(key string) bool {
	if s == nil {
		return false
	}
	setting, ok := s.Settings[key]
	return ok && (setting.Format != "" || len(setting.Values) != 0)
}

// ValidateTyped checks a value against the setting's declared type and numeric
// bounds. Callers log violations without rejecting the value: format and enum
// validation remain the only enforced kinds.
func (s *Schema) ValidateTyped(key, value string) error {
	if s == nil {
		return nil
	}
	setting, ok := s.Settings[key]
	if !ok {
		return nil
	}
	switch setting.Type {
	case "bool":
		if value != "true" && value != "false" {
			return fmt.Errorf("value %q is not a bool", value)
		}
		return nil
	case "int", "integer":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("value %q is not an int", value)
		}
		return checkBounds(setting, float64(parsed))
	case "float":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("value %q is not a float", value)
		}
		return checkBounds(setting, parsed)
	case "duration":
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("value %q is not a duration", value)
		}
		return checkBounds(setting, parsed.Seconds())
	default:
		// string, url, enum, and untyped settings carry no declared bounds.
		return nil
	}
}

func checkBounds(setting Setting, value float64) error {
	if setting.Min != nil && value < *setting.Min {
		return fmt.Errorf("value %v is below minimum %v", value, *setting.Min)
	}
	if setting.Max != nil && value > *setting.Max {
		return fmt.Errorf("value %v is above maximum %v", value, *setting.Max)
	}
	return nil
}

func (s *Schema) DefaultValue(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	setting, ok := s.Settings[key]
	if !ok || setting.Default == nil {
		return "", false
	}
	return fmt.Sprintf("%v", setting.Default), true
}

// ChannelDefaults returns defaults selected by any installed component channel.
// A true value wins if components are on different channels, so development
// capabilities remain available while either board is on testing or nightly.
func (s *Schema) ChannelDefaults(channels []string) map[string]string {
	defaults := make(map[string]string)
	if s == nil {
		return defaults
	}
	for key, setting := range s.Settings {
		matched := false
		value := false
		for _, channel := range channels {
			channelValue, ok := setting.ChannelDefaults[channel]
			if !ok {
				continue
			}
			matched = true
			value = value || channelValue
		}
		if matched {
			defaults[key] = fmt.Sprintf("%t", value)
		}
	}
	return defaults
}

// ReleaseChannel infers the installed release channel from an image version.
func ReleaseChannel(version string) string {
	fields := strings.Fields(version)
	if len(fields) == 0 {
		return ""
	}
	version = strings.ToLower(fields[0])
	switch {
	case strings.HasPrefix(version, "nightly-"), strings.HasPrefix(version, "custom-nightly-"):
		return "nightly"
	case strings.HasPrefix(version, "testing-"):
		return "testing"
	case strings.HasPrefix(version, "v"), version[0] >= '0' && version[0] <= '9':
		return "stable"
	default:
		return ""
	}
}

// Defaults includes transient defaults because transient controls persistence,
// not boot-time Redis hydration.
func (s *Schema) Defaults() map[string]string {
	defaults := make(map[string]string)
	for key, setting := range s.Settings {
		if setting.Default == nil {
			continue
		}
		switch v := setting.Default.(type) {
		case float64:
			if v == float64(int64(v)) {
				defaults[key] = fmt.Sprintf("%d", int64(v))
			} else {
				defaults[key] = fmt.Sprintf("%v", v)
			}
		case bool:
			defaults[key] = fmt.Sprintf("%v", v)
		default:
			defaults[key] = fmt.Sprintf("%v", v)
		}
	}
	return defaults
}
