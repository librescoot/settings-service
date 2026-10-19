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
	for _, key := range []string{
		"experimental.battery.dual-pack-admission",
		"experimental.battery.dual-pack-admission-while-stopped",
	} {
		t.Run(key, func(t *testing.T) {
			setting, ok := s.Settings[key]
			if !ok {
				t.Fatal("admission setting is missing")
			}
			if setting.Type != "bool" || setting.Default != false || setting.UserVisible || setting.ReadOnly || setting.Transient || len(setting.ChannelDefaults) != 0 {
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
			for _, value := range []string{"invalid", "TRUE", "1"} {
				if err := s.ValidateTyped(key, value); err == nil {
					t.Fatalf("invalid admission setting %q was accepted", value)
				}
			}
		})
	}
}
