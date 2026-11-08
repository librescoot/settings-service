package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/librescoot/settings-service/internal/config"
	"github.com/librescoot/settings-service/internal/redis"
)

func TestDataBrowserSettingLoadsOnlyFromTOML(t *testing.T) {
	for _, tc := range []struct {
		name, toml, want string
	}{
		{"missing", "", "false"},
		{"enabled", "[bluetooth]\ndata-browser = true\n", "true"},
		{"disabled", "[bluetooth]\ndata-browser = false\n", "false"},
		{"invalid value", "[bluetooth]\ndata-browser = \"yes\"\n", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := miniredis.RunT(t)
			previous := config.TomlFilePath
			config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
			t.Cleanup(func() { config.TomlFilePath = previous })
			if tc.toml != "" {
				if err := os.WriteFile(config.TomlFilePath, []byte(tc.toml), 0600); err != nil {
					t.Fatal(err)
				}
			}
			svc, err := New(server.Addr(), filepath.Join("..", "..", "settings.schema.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(svc.Close)
			if err := svc.LoadSettingsFromTOML(); err != nil {
				t.Fatal(err)
			}
			got, exists, err := svc.redisClient.GetSettingField(dataBrowserSetting)
			if err != nil || !exists || got != tc.want {
				t.Fatalf("setting = %q, exists=%v, err=%v; want %s", got, exists, err, tc.want)
			}
		})
	}
}

func TestDataBrowserRejectsRedisUpdatesAndPreservesTOML(t *testing.T) {
	server := miniredis.RunT(t)
	previous := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = previous })
	if err := os.WriteFile(config.TomlFilePath, []byte("[bluetooth]\ndata-browser = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	svc, err := New(server.Addr(), filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { svc.WatchSettings(); close(done) }()
	t.Cleanup(func() {
		svc.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("watch did not stop")
		}
	})
	waitForSettingsSubscription(t, server)
	server.HSet(redis.SettingsKey, dataBrowserSetting, "false")
	// Persisting a different setting must not copy an unnotified Redis edit.
	server.HSet(redis.SettingsKey, "alarm.enabled", "false")
	svc.markUserSet("alarm.enabled")
	if err := svc.SaveSettingsToTOML(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ToRedisFields()[dataBrowserSetting]; got != "true" {
		t.Fatalf("persisted %v, want true", got)
	}
	server.Publish(redis.SettingsChannel, dataBrowserSetting)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if value, _, _ := svc.redisClient.GetSettingField(dataBrowserSetting); value == "true" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if value, _, _ := svc.redisClient.GetSettingField(dataBrowserSetting); value != "true" {
		t.Fatal("Redis edit was not restored")
	}
	if err := os.WriteFile(config.TomlFilePath, []byte("[bluetooth]\ndata-browser = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server.HSet(redis.SettingsKey, "alarm.enabled", "true")
	if err := svc.SaveSettingsToTOML(); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadFromFile()
	if err != nil || cfg.ToRedisFields()[dataBrowserSetting] != "false" {
		t.Fatalf("local edit was not preserved: %v %v", cfg, err)
	}
	if value, _, _ := svc.redisClient.GetSettingField(dataBrowserSetting); value != "true" {
		t.Fatal("local edit took effect without reload")
	}
}
