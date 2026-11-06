package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/librescoot/settings-service/internal/config"
)

func TestLoadSettingsMigratesAlarmCooldownOnce(t *testing.T) {
	for _, tt := range []struct {
		name string
		toml string
		want string
	}{
		{"no settings file", "", "2"},
		{"no saved cooldown", "[alarm]\nhonk = true\n", "2"},
		{"numeric old default", "[alarm]\nl1-cooldown = 15\nhonk = true\n", "2"},
		{"string old default", "[alarm]\nl1-cooldown = \"15\"\nhonk = true\n", "2"},
		{"custom cooldown", "[alarm]\nl1-cooldown = 5\nhonk = true\n", "5"},
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
			}
			if tt.toml != "" {
				cfg, err := config.LoadFromFile()
				if err != nil {
					t.Fatal(err)
				}
				fields := cfg.ToRedisFields()
				if fields["alarm.honk"] != "true" {
					t.Fatal("migration changed unrelated alarm setting")
				}
				if tt.name != "no saved cooldown" && fields["alarm.l1-cooldown"] != tt.want {
					t.Fatalf("persisted cooldown = %v; want %s", fields["alarm.l1-cooldown"], tt.want)
				}
			}

			// A later explicit choice must survive both saving and hydration.
			server.HSet("settings", "alarm.l1-cooldown", "15")
			svc.userSetKeys["alarm.l1-cooldown"] = struct{}{}
			if err := svc.SaveSettingsToTOML(); err != nil {
				t.Fatal(err)
			}
			if err := svc.LoadSettingsFromTOML(); err != nil {
				t.Fatal(err)
			}
			got, _, err := svc.redisClient.GetSettingField("alarm.l1-cooldown")
			if err != nil || got != "15" {
				t.Fatalf("later explicit cooldown = %q, err=%v; want 15", got, err)
			}
		})
	}
}

func TestAlarmCooldownMigrationSaveFailureDoesNotMarkComplete(t *testing.T) {
	originalPath := config.TomlFilePath
	config.TomlFilePath = t.TempDir()
	t.Cleanup(func() { config.TomlFilePath = originalPath })
	cfg := config.Config{"alarm": map[string]interface{}{"l1-cooldown": 15}}
	if err := migrateAlarmCooldown(&cfg); err == nil {
		t.Fatal("expected failure saving settings over a directory")
	}
	if _, err := os.Stat(config.TomlFilePath + ".alarm-cooldown-v1"); !os.IsNotExist(err) {
		t.Fatalf("failed migration must not be marked complete: %v", err)
	}
}
