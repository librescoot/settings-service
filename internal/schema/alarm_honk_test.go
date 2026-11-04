package schema

import (
	"path/filepath"
	"testing"
)

func TestAlarmHonkDefaultsOn(t *testing.T) {
	s, err := LoadFile(filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	setting, ok := s.Settings["alarm.honk"]
	if !ok || setting.Type != "bool" || setting.Default != true || !setting.UserVisible || setting.Service != "alarm-service" {
		t.Fatalf("unexpected alarm honk setting: %+v", setting)
	}
	for _, value := range []string{"true", "false"} {
		if err := s.ValidateTyped("alarm.honk", value); err != nil {
			t.Fatal(err)
		}
	}
}
