package schema

import (
	"encoding/json"
	"fmt"
	"os"
)

type EnumValue struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type Setting struct {
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Label       string      `json:"label,omitempty"`
	UserVisible bool        `json:"user-visible,omitempty"`
	Service     string      `json:"service,omitempty"`
	Default     any         `json:"default,omitempty"`
	Values      []EnumValue `json:"values,omitempty"`
	Unit        string      `json:"unit,omitempty"`
	Min         *float64    `json:"min,omitempty"`
	Max         *float64    `json:"max,omitempty"`
	Example     any         `json:"example,omitempty"`
	ReadOnly    bool        `json:"read-only,omitempty"`
	Pattern     string      `json:"pattern,omitempty"`
	Format      string      `json:"format,omitempty"`
	Transient   bool        `json:"transient,omitempty"`
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
