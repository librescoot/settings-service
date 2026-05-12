package schema

import (
	"encoding/json"
	"fmt"
	"os"
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

// IsTransient reports whether the named setting is declared transient in
// the schema. Transient settings live only in Redis: they are never read
// from or written to /data/settings.toml. Schema defaults still hydrate
// into Redis at boot — the transient flag only governs persistence, not
// default application. A runtime HSET overrides the default for the
// current session; the next reboot wipes Redis and the default loads
// again.
//
// Use cases:
//   - default: null + transient: true → no value at boot, services infer
//     from elsewhere (e.g. updates.{mdb,dbc}.channel from VERSION_ID).
//   - default: "x" + transient: true → "x" loads on every boot, runtime
//     overrides don't survive (e.g. scooter.usb0-policy = "auto" with
//     "always-on" as a temporary override).
//
// Returns false when the schema is nil or the key is unknown — both safe
// defaults that preserve the legacy persist-everything behavior.
func (s *Schema) IsTransient(key string) bool {
	if s == nil {
		return false
	}
	setting, ok := s.Settings[key]
	return ok && setting.Transient
}

// Defaults returns every schema-declared default value. Transient keys
// are included when they declare a default: transient governs persistence
// (no toml load/save) but not boot-time hydration into Redis. A transient
// key with default "auto" gets "auto" on every boot, then runtime HSETs
// can override it for the session, and the next reboot returns to "auto".
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
