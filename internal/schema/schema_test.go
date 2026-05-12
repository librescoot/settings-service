package schema

import (
	"testing"
)

const testJSON = `{
  "alarm.enabled": {
    "type": "bool",
    "description": "Enable or disable the alarm system",
    "label": "Alarm",
    "user-visible": true,
    "service": "alarm-service",
    "default": false
  },
  "alarm.duration": {
    "type": "int",
    "description": "Duration in seconds for alarm sound",
    "label": "Alarm Duration",
    "user-visible": true,
    "service": "alarm-service",
    "unit": "seconds",
    "min": 0,
    "max": 300,
    "default": 60
  },
  "dashboard.theme": {
    "type": "enum",
    "description": "UI theme",
    "label": "Theme",
    "user-visible": true,
    "service": "scootui",
    "values": [
      {"value": "light", "label": "Light"},
      {"value": "dark", "label": "Dark"},
      {"value": "auto", "label": "Auto"}
    ],
    "default": "dark"
  },
  "cellular.apn": {
    "type": "string",
    "description": "Cellular APN string",
    "service": "modem-service"
  }
}`

func TestParse(t *testing.T) {
	s, err := Parse([]byte(testJSON))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if len(s.Settings) != 4 {
		t.Fatalf("expected 4 settings, got %d", len(s.Settings))
	}

	// alarm.enabled
	ae := s.Settings["alarm.enabled"]
	if ae.Type != "bool" {
		t.Errorf("alarm.enabled type = %q, want %q", ae.Type, "bool")
	}
	if ae.Description != "Enable or disable the alarm system" {
		t.Errorf("alarm.enabled description = %q", ae.Description)
	}
	if ae.Label != "Alarm" {
		t.Errorf("alarm.enabled label = %q, want %q", ae.Label, "Alarm")
	}
	if !ae.UserVisible {
		t.Error("alarm.enabled should be user-visible")
	}
	if ae.Service != "alarm-service" {
		t.Errorf("alarm.enabled service = %q, want %q", ae.Service, "alarm-service")
	}
	if ae.Default != false {
		t.Errorf("alarm.enabled default = %v, want false", ae.Default)
	}

	// alarm.duration
	ad := s.Settings["alarm.duration"]
	if ad.Type != "int" {
		t.Errorf("alarm.duration type = %q, want %q", ad.Type, "int")
	}
	if ad.Label != "Alarm Duration" {
		t.Errorf("alarm.duration label = %q, want %q", ad.Label, "Alarm Duration")
	}
	if ad.Unit != "seconds" {
		t.Errorf("alarm.duration unit = %q, want %q", ad.Unit, "seconds")
	}
	if ad.Min == nil || *ad.Min != 0 {
		t.Errorf("alarm.duration min = %v, want 0", ad.Min)
	}
	if ad.Max == nil || *ad.Max != 300 {
		t.Errorf("alarm.duration max = %v, want 300", ad.Max)
	}
	if ad.Default != float64(60) {
		t.Errorf("alarm.duration default = %v, want 60", ad.Default)
	}

	// dashboard.theme (enum with values)
	dt := s.Settings["dashboard.theme"]
	if dt.Type != "enum" {
		t.Errorf("dashboard.theme type = %q, want %q", dt.Type, "enum")
	}
	if !dt.UserVisible {
		t.Error("dashboard.theme should be user-visible")
	}
	if len(dt.Values) != 3 {
		t.Fatalf("dashboard.theme values count = %d, want 3", len(dt.Values))
	}
	if dt.Values[0].Value != "light" || dt.Values[0].Label != "Light" {
		t.Errorf("dashboard.theme values[0] = %+v", dt.Values[0])
	}
	if dt.Values[1].Value != "dark" || dt.Values[1].Label != "Dark" {
		t.Errorf("dashboard.theme values[1] = %+v", dt.Values[1])
	}
	if dt.Values[2].Value != "auto" || dt.Values[2].Label != "Auto" {
		t.Errorf("dashboard.theme values[2] = %+v", dt.Values[2])
	}
	if dt.Default != "dark" {
		t.Errorf("dashboard.theme default = %v, want %q", dt.Default, "dark")
	}

	// cellular.apn (no default, no label, not user-visible)
	ca := s.Settings["cellular.apn"]
	if ca.Type != "string" {
		t.Errorf("cellular.apn type = %q, want %q", ca.Type, "string")
	}
	if ca.Label != "" {
		t.Errorf("cellular.apn label = %q, want empty", ca.Label)
	}
	if ca.UserVisible {
		t.Error("cellular.apn should not be user-visible")
	}
	if ca.Default != nil {
		t.Errorf("cellular.apn default = %v, want nil", ca.Default)
	}
}

func TestDefaults(t *testing.T) {
	s, err := Parse([]byte(testJSON))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	defaults := s.Defaults()

	if len(defaults) != 3 {
		t.Fatalf("expected 3 defaults, got %d: %v", len(defaults), defaults)
	}

	expected := map[string]string{
		"alarm.enabled":   "false",
		"alarm.duration":  "60",
		"dashboard.theme": "dark",
	}

	for key, want := range expected {
		got, ok := defaults[key]
		if !ok {
			t.Errorf("defaults missing key %q", key)
			continue
		}
		if got != want {
			t.Errorf("defaults[%q] = %q, want %q", key, got, want)
		}
	}

	if _, ok := defaults["cellular.apn"]; ok {
		t.Error("defaults should not contain cellular.apn (no default)")
	}
}

func TestTransient(t *testing.T) {
	const transientJSON = `{
  "alarm.enabled": {
    "type": "bool",
    "default": true
  },
  "updates.mdb.channel": {
    "type": "enum",
    "default": null,
    "transient": true
  },
  "scooter.usb0-policy": {
    "type": "enum",
    "default": "auto",
    "transient": true
  }
}`

	s, err := Parse([]byte(transientJSON))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if !s.IsTransient("updates.mdb.channel") {
		t.Error("updates.mdb.channel should be transient")
	}
	if !s.IsTransient("scooter.usb0-policy") {
		t.Error("scooter.usb0-policy should be transient")
	}
	if s.IsTransient("alarm.enabled") {
		t.Error("alarm.enabled should not be transient")
	}
	if s.IsTransient("does.not.exist") {
		t.Error("unknown keys should not be transient")
	}

	var nilSchema *Schema
	if nilSchema.IsTransient("anything") {
		t.Error("nil schema should report nothing transient")
	}

	defaults := s.Defaults()

	// Transient keys without a default are skipped.
	if _, ok := defaults["updates.mdb.channel"]; ok {
		t.Error("Defaults() must skip transient keys with no default")
	}
	// Transient keys WITH a default are hydrated — that's the whole
	// point of decoupling the two concerns.
	if defaults["scooter.usb0-policy"] != "auto" {
		t.Errorf("Defaults()[scooter.usb0-policy] = %q, want \"auto\" (transient + default should hydrate)", defaults["scooter.usb0-policy"])
	}
	if defaults["alarm.enabled"] != "true" {
		t.Errorf("Defaults() should keep non-transient keys, got %v", defaults)
	}
}

func TestRawBytes(t *testing.T) {
	data := []byte(testJSON)
	s, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if string(s.Raw) != testJSON {
		t.Error("Raw bytes do not match input")
	}
}
