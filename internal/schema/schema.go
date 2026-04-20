package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type EnumValue struct {
	Value string `json:"value"`
	Label string `json:"label"`
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
}

// patternSetting is a schema entry whose key contains wildcard segments.
type patternSetting struct {
	segments []string
	setting  Setting
}

type Schema struct {
	// Settings holds exact-match entries, keyed by fully-dotted key.
	Settings map[string]Setting
	// Patterns holds entries whose key has at least one "*" segment, e.g.
	// "dashboard.saved-locations.*.latitude". Exact matches in Settings
	// take precedence over patterns during lookup.
	Patterns []patternSetting
	Raw      []byte
}

func Parse(data []byte) (*Schema, error) {
	var raw map[string]Setting
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing schema: %w", err)
	}
	s := &Schema{
		Settings: make(map[string]Setting),
		Raw:      data,
	}
	for key, setting := range raw {
		segs := strings.Split(key, ".")
		wildcard := false
		for _, seg := range segs {
			if seg == "*" {
				wildcard = true
				break
			}
		}
		if wildcard {
			s.Patterns = append(s.Patterns, patternSetting{segments: segs, setting: setting})
		} else {
			s.Settings[key] = setting
		}
	}
	return s, nil
}

// Lookup finds the Setting matching key, preferring exact matches over patterns.
func (s *Schema) Lookup(key string) (Setting, bool) {
	if setting, ok := s.Settings[key]; ok {
		return setting, true
	}
	segs := strings.Split(key, ".")
	for _, p := range s.Patterns {
		if matchSegments(p.segments, segs) {
			return p.setting, true
		}
	}
	return Setting{}, false
}

// Has reports whether key is covered by the schema (exact or wildcard).
func (s *Schema) Has(key string) bool {
	_, ok := s.Lookup(key)
	return ok
}

func matchSegments(pattern, key []string) bool {
	if len(pattern) != len(key) {
		return false
	}
	for i, p := range pattern {
		if p != "*" && p != key[i] {
			return false
		}
	}
	return true
}

func LoadFile(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading schema file: %w", err)
	}
	return Parse(data)
}

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
