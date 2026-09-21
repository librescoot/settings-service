package schema

import (
	"encoding/json"
	"path/filepath"
	"strings"
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
      {"value": "auto", "label": "Auto", "description": "Follow ambient conditions."}
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
	if dt.Values[2].Description != "Follow ambient conditions." {
		t.Errorf("dashboard.theme values[2].Description = %q", dt.Values[2].Description)
	}
	if dt.Default != "dark" {
		t.Errorf("dashboard.theme default = %v, want %q", dt.Default, "dark")
	}

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

func TestChannelDefaults(t *testing.T) {
	const settingsJSON = `{
  "scooter.developer-mode": {
    "type": "bool",
    "default": false,
    "channel-defaults": {
      "stable": false,
      "testing": true,
      "nightly": true
    }
  }
}`
	s, err := Parse([]byte(settingsJSON))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	for _, tc := range []struct {
		name     string
		channels []string
		want     string
	}{
		{"stable", []string{"stable"}, "false"},
		{"testing", []string{"testing"}, "true"},
		{"nightly", []string{"nightly"}, "true"},
		{"mixed components", []string{"stable", "testing"}, "true"},
		{"unknown", []string{"unknown"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := s.ChannelDefaults(tc.channels)["scooter.developer-mode"]
			if got != tc.want {
				t.Errorf("ChannelDefaults(%v) = %q, want %q", tc.channels, got, tc.want)
			}
		})
	}
}

func TestReleaseChannel(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    string
	}{
		{"nightly-20260921T120000", "nightly"},
		{"custom-nightly-20260921T120000-test-branch", "nightly"},
		{"testing-20260921T120000", "testing"},
		{"v1.4.0", "stable"},
		{"1.4.0", "stable"},
		{"", ""},
		{"custom-20260921", ""},
	} {
		if got := ReleaseChannel(tc.version); got != tc.want {
			t.Errorf("ReleaseChannel(%q) = %q, want %q", tc.version, got, tc.want)
		}
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

	if _, ok := defaults["updates.mdb.channel"]; ok {
		t.Error("Defaults() must skip transient keys with no default")
	}

	if defaults["scooter.usb0-policy"] != "auto" {
		t.Errorf("Defaults()[scooter.usb0-policy] = %q, want \"auto\" (transient + default should hydrate)", defaults["scooter.usb0-policy"])
	}
	if defaults["alarm.enabled"] != "true" {
		t.Errorf("Defaults() should keep non-transient keys, got %v", defaults)
	}
}

func TestTripCounterResetSchema(t *testing.T) {
	s, err := LoadFile(filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}

	setting, ok := s.Settings["trip.counter-reset"]
	if !ok {
		t.Fatal("trip.counter-reset is missing")
	}
	if setting.Type != "enum" {
		t.Errorf("type = %q, want %q", setting.Type, "enum")
	}
	if !setting.UserVisible {
		t.Error("trip.counter-reset should be user-visible")
	}
	if setting.ReadOnly {
		t.Error("trip.counter-reset should be writable")
	}
	if setting.Service != "trip-service" {
		t.Errorf("service = %q, want %q", setting.Service, "trip-service")
	}
	if setting.Default != "ride" {
		t.Errorf("default = %v, want %q", setting.Default, "ride")
	}
	if got := s.Defaults()["trip.counter-reset"]; got != "ride" {
		t.Errorf("Defaults()[trip.counter-reset] = %q, want %q", got, "ride")
	}

	if len(setting.Values) != 4 {
		t.Errorf("values count = %d, want 4", len(setting.Values))
	}
	for _, value := range []string{"ride", "day", "battery", "manual"} {
		if !enumContains(setting.Values, value) {
			t.Errorf("valid value %q is missing", value)
		}
	}
	if enumContains(setting.Values, "unknown") {
		t.Error("unknown must be rejected by enum validation")
	}
	for _, value := range []string{"ride", "day", "battery", "manual"} {
		if err := s.ValidateValue("trip.counter-reset", value); err != nil {
			t.Errorf("ValidateValue(%q) error: %v", value, err)
		}
	}
	if err := s.ValidateValue("trip.counter-reset", "sometimes"); err == nil {
		t.Error("ValidateValue accepted invalid counter reset policy")
	}
	if !s.HasValidation("trip.counter-reset") {
		t.Error("trip.counter-reset should require production validation")
	}
	if got, ok := s.DefaultValue("trip.counter-reset"); !ok || got != "ride" {
		t.Errorf("DefaultValue(trip.counter-reset) = %q, %v; want ride, true", got, ok)
	}
}

func TestTripExpungeSchema(t *testing.T) {
	s, err := LoadFile(filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}

	setting, ok := s.Settings["trip.expunge"]
	if !ok {
		t.Fatal("trip.expunge is missing")
	}
	if setting.Type != "string" {
		t.Errorf("type = %q, want %q", setting.Type, "string")
	}
	if setting.Format != TripExpungeFormat {
		t.Errorf("format = %q, want %q", setting.Format, TripExpungeFormat)
	}
	if !setting.UserVisible {
		t.Error("trip.expunge should be user-visible")
	}
	if setting.ReadOnly {
		t.Error("trip.expunge should be writable")
	}
	if setting.Service != "trip-service" {
		t.Errorf("service = %q, want %q", setting.Service, "trip-service")
	}
	if setting.Default != "age:365d" {
		t.Errorf("default = %v, want %q", setting.Default, "age:365d")
	}
	if got := s.Defaults()["trip.expunge"]; got != "age:365d" {
		t.Errorf("Defaults()[trip.expunge] = %q, want %q", got, "age:365d")
	}
}

func enumContains(values []EnumValue, want string) bool {
	for _, value := range values {
		if value.Value == want {
			return true
		}
	}
	return false
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

func TestValidateTyped(t *testing.T) {
	s, err := Parse([]byte(`{
  "alarm.enabled": {"type": "bool", "description": "d", "default": false},
  "alarm.duration": {"type": "int", "description": "d", "min": 0, "max": 300, "default": 60},
  "battery.temperature": {"type": "float", "description": "d", "min": -90, "max": 90, "default": 20},
  "updates.check-interval": {"type": "duration", "description": "d", "min": 0, "default": "6h"},
  "dashboard.language": {"type": "string", "description": "d", "default": "en"},
  "dashboard.theme": {"type": "enum", "description": "d", "default": "auto",
    "values": [{"value": "auto", "label": "Auto"}]}
}`))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	for _, tc := range []struct {
		key   string
		value string
	}{
		{"alarm.enabled", "true"},
		{"alarm.enabled", "false"},
		{"alarm.duration", "0"},
		{"alarm.duration", "300"},
		{"battery.temperature", "-89.5"},
		{"battery.temperature", "20"},
		{"updates.check-interval", "6h"},
		{"updates.check-interval", "0"},
		{"dashboard.language", "anything goes"},
		{"dashboard.theme", "not-checked-by-type"},
		{"unknown.setting", "whatever"},
	} {
		if err := s.ValidateTyped(tc.key, tc.value); err != nil {
			t.Errorf("ValidateTyped(%q, %q) error: %v", tc.key, tc.value, err)
		}
	}

	for _, tc := range []struct {
		key   string
		value string
	}{
		{"alarm.enabled", "yes"},
		{"alarm.enabled", "1"},
		{"alarm.duration", "30.5"},
		{"alarm.duration", "banana"},
		{"alarm.duration", "-1"},
		{"alarm.duration", "301"},
		{"battery.temperature", "banana"},
		{"battery.temperature", "-90.1"},
		{"battery.temperature", "91"},
		{"updates.check-interval", "10x"},
		{"updates.check-interval", "-1s"},
	} {
		if err := s.ValidateTyped(tc.key, tc.value); err == nil {
			t.Errorf("ValidateTyped(%q, %q) accepted an invalid value", tc.key, tc.value)
		}
	}

	var nilSchema *Schema
	if err := nilSchema.ValidateTyped("alarm.enabled", "yes"); err != nil {
		t.Errorf("nil schema ValidateTyped error: %v", err)
	}
}

// Every shipped default must satisfy its own declared type and bounds, so a
// schema entry can never mislabel a value the service then flags at boot.
func TestDefaultsRespectDeclaredTypes(t *testing.T) {
	s, err := LoadFile(filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}
	checked := 0
	for key, value := range s.Defaults() {
		if err := s.ValidateTyped(key, value); err != nil {
			t.Errorf("default for %s (%q) violates its declared type: %v", key, value, err)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no defaults were checked")
	}
}

func TestValidateShortcutItems(t *testing.T) {
	uuid := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	uuid2 := "0d6c21f0-0000-4000-8000-000000000001"

	for _, value := range []string{
		`[]`,
		`["view","theme"]`,
		`["view","theme","debug-overlay","motion-debug","route-overview","skip-stop","stop-navigation"]`,
		`["destination:` + uuid + `:home"]`,
		`["view","destination:` + uuid + `:place","theme"]`,
		`["destination:` + uuid + `:home","destination:` + uuid2 + `:work"]`,
		`["destination:` + strings.ToUpper(uuid) + `:favorite"]`,
	} {
		if err := ValidateShortcutItems(value); err != nil {
			t.Errorf("ValidateShortcutItems(%q) error: %v", value, err)
		}
	}

	for _, value := range []string{
		`"view"`,
		`[1,2]`,
		`[""]`,
		`["vew"]`,
		`["view","view"]`,
		`["destination:notauuid:home"]`,
		`["destination:` + uuid + `"]`,
		`["destination:` + uuid + `:skyscraper"]`,
		`["destination:` + uuid + `:home:extra"]`,
		`["destination:` + uuid + `:home","destination:` + strings.ToUpper(uuid) + `:work"]`,
	} {
		if err := ValidateShortcutItems(value); err == nil {
			t.Errorf("ValidateShortcutItems(%q) accepted an invalid value", value)
		}
	}

	tooLong := make([]string, maxShortcutItems+1)
	for i := range tooLong {
		tooLong[i] = "view"
	}
	tooLong[1] = "theme"
	data, _ := json.Marshal(tooLong)
	if err := ValidateShortcutItems(string(data)); err == nil {
		t.Errorf("ValidateShortcutItems accepted %d entries", maxShortcutItems+1)
	}
}

// The shipped default must be valid items JSON so hydration, live repair, and
// the dashboard's fallback all agree on one list.
func TestShortcutItemsDefaultIsValidJSONArray(t *testing.T) {
	s, err := LoadFile(filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}
	value, ok := s.DefaultValue("dashboard.shortcut-menu.items")
	if !ok {
		t.Fatal("dashboard.shortcut-menu.items has no default")
	}
	if err := ValidateShortcutItems(value); err != nil {
		t.Errorf("default %q is invalid: %v", value, err)
	}
	if value != `["view","theme","debug-overlay","motion-debug","route-overview","skip-stop","stop-navigation"]` {
		t.Errorf("default = %q, want the seven fixed actions in order", value)
	}
}
