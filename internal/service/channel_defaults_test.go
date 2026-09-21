package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/librescoot/settings-service/internal/config"
)

func TestReadOSReleaseVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    string
	}{
		{"unquoted", "NAME=Librescoot\nVERSION_ID=nightly-20260921T120000\n", "nightly-20260921T120000"},
		{"double quoted", "VERSION_ID=\"testing-20260921T120000\"\n", "testing-20260921T120000"},
		{"single quoted", "VERSION_ID='v1.4.0'\n", "v1.4.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "os-release")
			if err := os.WriteFile(path, []byte(tc.content), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := readOSReleaseVersion(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("readOSReleaseVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChannelDefaultsReadDBCMetadataWhenDashboardBecomesReady(t *testing.T) {
	server := miniredis.RunT(t)
	dir := t.TempDir()
	osReleasePath := filepath.Join(dir, "os-release")
	if err := os.WriteFile(osReleasePath, []byte("VERSION_ID=v1.4.0\n"), 0644); err != nil {
		t.Fatal(err)
	}

	originalTomlPath := config.TomlFilePath
	config.TomlFilePath = filepath.Join(dir, "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = originalTomlPath })

	svc, err := New(server.Addr(), filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc.osReleasePath = osReleasePath
	t.Cleanup(svc.Close)
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatal(err)
	}
	assertSettingValue(t, svc, "scooter.developer-mode", "false")

	go svc.WatchSettings()
	waitForSettingsSubscription(t, server)
	server.HSet("version:dbc", "version_id", "nightly-20260921T120000")
	time.Sleep(20 * time.Millisecond)
	assertSettingValue(t, svc, "scooter.developer-mode", "false")

	server.HSet("dashboard", "ready", "true")
	server.Publish("dashboard", "ready")
	waitForSettingValue(t, svc, "scooter.developer-mode", "true")
	waitForGeneratedNotifications(t, svc)

	if _, err := os.Stat(config.TomlFilePath); !os.IsNotExist(err) {
		t.Fatalf("generated channel default was persisted to TOML: %v", err)
	}

	if err := svc.redisClient.SetSettingField("scooter.developer-mode", "false"); err != nil {
		t.Fatal(err)
	}
	waitForPersistedSetting(t, svc, "scooter.developer-mode", "false")
	server.Publish("dashboard", "ready")
	time.Sleep(20 * time.Millisecond)
	assertSettingValue(t, svc, "scooter.developer-mode", "false")
}

func assertSettingValue(t *testing.T, svc *SettingsService, key, want string) {
	t.Helper()
	got, exists, err := svc.redisClient.GetSettingField(key)
	if err != nil || !exists || got != want {
		t.Fatalf("%s = %q, exists=%v, err=%v; want %q", key, got, exists, err, want)
	}
}

func waitForSettingValue(t *testing.T, svc *SettingsService, key, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, exists, err := svc.redisClient.GetSettingField(key)
		if err == nil && exists && got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s=%q", key, want)
}

func waitForGeneratedNotifications(t *testing.T, svc *SettingsService) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		svc.mu.Lock()
		pending := len(svc.pendingGeneratedNotifications)
		svc.mu.Unlock()
		if pending == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for generated settings notification")
}
