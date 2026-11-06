package schema

import (
	"path/filepath"
	"testing"
)

func TestAlarmQuietTimeIsAdvancedAndBounded(t *testing.T) {
	s, err := LoadFile(filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	setting, ok := s.Settings["alarm.quiet-time"]
	if !ok || setting.Type != "int" || setting.Default != float64(30) || setting.UserVisible || setting.Service != "alarm-service" {
		t.Fatalf("unexpected quiet-time setting: %+v", setting)
	}
	for _, value := range []string{"1", "30", "300"} {
		if err := s.ValidateTyped("alarm.quiet-time", value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"0", "301", "-1", "1.5", "30seconds"} {
		if err := s.ValidateTyped("alarm.quiet-time", value); err == nil {
			t.Fatalf("accepted invalid quiet time %q", value)
		}
	}
}
