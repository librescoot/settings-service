package schema

import (
	"path/filepath"
	"testing"
)

func TestAlarmLEDDefaultsOff(t *testing.T) {
	s, err := LoadFile(filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	setting, ok := s.Settings["alarm.armed-led"]
	if !ok || setting.Type != "bool" || setting.Default != false || !setting.UserVisible || setting.Service != "keycard-service" {
		t.Fatalf("unexpected alarm LED setting: %+v", setting)
	}
	for _, value := range []string{"true", "false"} {
		if err := s.ValidateTyped("alarm.armed-led", value); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ValidateTyped("alarm.armed-led", "enabled"); err == nil {
		t.Fatal("non-boolean alarm LED setting accepted")
	}
}
