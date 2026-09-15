package config

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestTripSettingsTOMLRoundTrip(t *testing.T) {
	cfg := ParseRedisSettings(map[string]string{
		"trip.counter-reset": "manual",
		"trip.expunge":       "age:365d",
	})

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
