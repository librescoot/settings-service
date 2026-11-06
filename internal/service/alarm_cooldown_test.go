package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/librescoot/settings-service/internal/config"
)

func TestAlarmCooldownDefaultPreservesExplicitSettings(t *testing.T) {
	for _, tt := range []struct {
		name     string
		toml     string
		want     string
		explicit bool
	}{
		{"no settings file", "", "2", false},
		{"no saved cooldown", "[alarm]\nhonk = true\n", "2", false},
		{"numeric explicit 15", "[alarm]\nl1-cooldown = 15\n", "15", true},
		{"string explicit 15", "[alarm]\nl1-cooldown = \"15\"\n", "15", true},
		{"explicit matching default", "[alarm]\nl1-cooldown = 2\n", "2", true},
		{"custom cooldown", "[alarm]\nl1-cooldown = 5\n", "5", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			originalPath := config.TomlFilePath
			config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
			t.Cleanup(func() { config.TomlFilePath = originalPath })
			if tt.toml != "" {
				if err := os.WriteFile(config.TomlFilePath, []byte(tt.toml), 0600); err != nil {
					t.Fatal(err)
				}
			}
			server := miniredis.RunT(t)
			svc, err := New(server.Addr(), filepath.Join("..", "..", "settings.schema.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(svc.Close)

			for range 2 {
				if err := svc.LoadSettingsFromTOML(); err != nil {
					t.Fatal(err)
				}
				got, exists, err := svc.redisClient.GetSettingField("alarm.l1-cooldown")
				if err != nil || !exists || got != tt.want {
					t.Fatalf("cooldown = %q, exists=%v, err=%v; want %q", got, exists, err, tt.want)
				}

				// Saving an unrelated edit must not turn a default into a user choice.
				server.HSet("settings", "alarm.honk", "true")
				svc.markUserSet("alarm.honk")
				if err := svc.SaveSettingsToTOML(); err != nil {
					t.Fatal(err)
				}
				cfg, err := config.LoadFromFile()
				if err != nil {
					t.Fatal(err)
				}
				fields := cfg.ToRedisFields()
				value, saved := fields["alarm.l1-cooldown"]
				if saved != tt.explicit || (saved && value != tt.want) {
					t.Fatalf("persisted cooldown = %v, saved=%v; want %q, saved=%v", value, saved, tt.want, tt.explicit)
				}
				if fields["alarm.honk"] != "true" {
					t.Fatal("unrelated setting was not persisted")
				}
			}
		})
	}
}
