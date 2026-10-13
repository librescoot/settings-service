package schema

import (
	"path/filepath"
	"testing"
)

func TestBatteryAdmissionDefaultsOff(t *testing.T) {
	s, err := LoadFile(filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	const key = "experimental.battery.dual-pack-admission"
	setting, ok := s.Settings[key]
	if !ok {
		t.Fatal("admission setting is missing")
	}
	if setting.Type != "bool" || setting.Default != false || setting.UserVisible || setting.ReadOnly {
		t.Fatalf("unexpected admission setting: %+v", setting)
	}
	if got := s.Defaults()[key]; got != "false" {
		t.Fatalf("default = %q, want false", got)
	}
	for _, value := range []string{"true", "false"} {
		if err := s.ValidateTyped(key, value); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
	if err := s.ValidateTyped(key, "invalid"); err == nil {
		t.Fatal("invalid admission setting was accepted")
	}
}
