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

func TestWatchSettingsKeepsMotionDebugMode(t *testing.T) {
	server := miniredis.RunT(t)
	originalTomlPath := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = originalTomlPath })

	svc, err := New(server.Addr(), filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadSettingsFromTOML(); err != nil {
		svc.Close()
		t.Fatal(err)
	}
	watchDone := make(chan struct{})
	go func() { svc.WatchSettings(); close(watchDone) }()
	t.Cleanup(func() {
		svc.Close()
		select {
		case <-watchDone:
		case <-time.After(time.Second):
			t.Error("WatchSettings did not stop")
		}
	})
	waitForSettingsSubscription(t, server)

	const key = "dashboard.mode"
	for _, mode := range []string{"speedometer", "navigation", "debug", "motion-debug"} {
		server.HSet(redis.SettingsKey, key, mode)
		server.Publish(redis.SettingsChannel, key)
		waitForPersistedSetting(t, svc, key, mode)
	}

	server.HSet(redis.SettingsKey, key, "invalid-mode")
	server.Publish(redis.SettingsChannel, key)
	waitForPersistedSetting(t, svc, key, "motion-debug")
}

func TestLoadSettingsFromTOMLKeepsMotionDebugMode(t *testing.T) {
	server := miniredis.RunT(t)
	originalTomlPath := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = originalTomlPath })
	if err := os.WriteFile(config.TomlFilePath, []byte("[dashboard]\nmode = \"motion-debug\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	svc, err := New(server.Addr(), filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatal(err)
	}
	waitForPersistedSetting(t, svc, "dashboard.mode", "motion-debug")
}
