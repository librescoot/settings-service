package schema

import (
	"path/filepath"
	"testing"
)

func TestAlarmHibernateLEDDefaultsOnAsAdvancedSetting(t *testing.T) {
	s, err := LoadFile(filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	setting, ok := s.Settings["alarm.armed-led-hibernate"]
	if !ok || setting.Type != "bool" || setting.Default != true || setting.UserVisible || setting.Service != "bluetooth-service" {
		t.Fatalf("unexpected hibernation LED setting: %+v", setting)
	}
	if s.Settings["alarm.armed-led"].Default != false {
		t.Fatal("general alarm LED must remain opt-in")
	}
	for _, value := range []string{"true", "false"} {
		if err := s.ValidateTyped("alarm.armed-led-hibernate", value); err != nil {
			t.Fatal(err)
		}
	}
}
