package config

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/librescoot/settings-service/internal/schema"
)

func TestTripSettingsTOMLRoundTrip(t *testing.T) {
	cfg := ParseRedisSettings(map[string]string{
		"trip.counter-reset": "manual",
		"trip.expunge":       "age:365d",
	}, nil)

	var data bytes.Buffer
	if err := toml.NewEncoder(&data).Encode(cfg); err != nil {
		t.Fatalf("encoding TOML: %v", err)
	}
	if !strings.Contains(data.String(), "[trip]\n") {
		t.Errorf("TOML = %q, want trip section", data.String())
	}

	var decoded Config
	if err := toml.Unmarshal(data.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding TOML: %v", err)
	}
	fields := decoded.ToRedisFields()
	if got := fields["trip.counter-reset"]; got != "manual" {
		t.Errorf("trip.counter-reset = %v, want %q", got, "manual")
	}
	if got := fields["trip.expunge"]; got != "age:365d" {
		t.Errorf("trip.expunge = %v, want %q", got, "age:365d")
	}
}

// Array-typed settings must land in TOML as a native ordered array, not a
// JSON string, and come back to Redis in the compact JSON wire form.
func TestArbitrarySchemaNamespaceTOMLRoundTrip(t *testing.T) {
	sch, err := schema.LoadFile(filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}

	const key = "experimental.battery.dual-pack-admission"
	cfg := ParseRedisSettings(map[string]string{key: "true"}, sch)
	data, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatalf("encoding TOML: %v", err)
	}
	if !strings.Contains(string(data), "[experimental.battery]") {
		t.Fatalf("TOML = %q, want nested experimental.battery table", data)
	}

	var decoded Config
	if err := toml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decoding TOML: %v", err)
	}
	if got := decoded.ToRedisFields()[key]; got != "true" {
		t.Fatalf("%s = %v, want true", key, got)
	}
}

func TestSchemaNamespaceTOMLRoundTripWithoutKnownSection(t *testing.T) {
	cfg := ParseRedisSettings(map[string]string{
		"future.namespace.setting": "value",
	}, nil)
	data, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatalf("encoding TOML: %v", err)
	}
	var decoded Config
	if err := toml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decoding TOML: %v", err)
	}
	if got := decoded.ToRedisFields()["future.namespace.setting"]; got != "value" {
		t.Fatalf("future namespace setting = %v, want value", got)
	}
}

func TestShortcutMenuItemsTOMLRoundTrip(t *testing.T) {
	sch, err := schema.LoadFile(filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}

	cfg := ParseRedisSettings(map[string]string{
		"dashboard.shortcut-menu.items": `["theme","view"]`,
	}, sch)

	var data bytes.Buffer
	if err := toml.NewEncoder(&data).Encode(cfg); err != nil {
		t.Fatalf("encoding TOML: %v", err)
	}
	if !strings.Contains(data.String(), `items = ["theme", "view"]`) {
		t.Errorf("TOML = %q, want native items array", data.String())
	}

	var decoded Config
	if err := toml.Unmarshal(data.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding TOML: %v", err)
	}
	fields := decoded.ToRedisFields()
	if got := fields["dashboard.shortcut-menu.items"]; got != `["theme","view"]` {
		t.Errorf("items = %v, want compact JSON [\"theme\",\"view\"]", got)
	}
}
