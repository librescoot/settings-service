package service

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/librescoot/settings-service/internal/config"
	settingsredis "github.com/librescoot/settings-service/internal/redis"
	"github.com/librescoot/settings-service/internal/schema"
	goredis "github.com/redis/go-redis/v9"
)

func TestMain(m *testing.M) {
	// Hydration also schedules journal-upload work; tests must never reach host systemd.
	dir, err := os.MkdirTemp("", "settings-test-commands-")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(dir+"/systemctl", []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		panic(err)
	}
	if err := os.Setenv("PATH", dir); err != nil {
		panic(err)
	}
	status := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(status)
}

func TestAPNHasNoCompetingNetworkWriter(t *testing.T) {
	tree, err := parser.ParseFile(token.NewFileSet(), "service.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range tree.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if path == "github.com/librescoot/settings-service/internal/network" {
			t.Fatal("settings persistence must not compete with modem-service APN transactions")
		}
	}
	ast.Inspect(tree, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && (ident.Name == "updateAPNFromRedis" || ident.Name == "UpdateAPN") {
			t.Error("APN application belongs to modem-service")
		}
		return true
	})
}

func TestAPNHydrationPublishesEmptyAndConfiguredValues(t *testing.T) {
	for _, apn := range []string{"", "example.apn"} {
		t.Run("apn="+apn, func(t *testing.T) {
			svc, server := newLogPrivacyService(t, map[string]schema.Setting{"cellular.apn": {Type: "string"}})
			client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			sub := client.Subscribe(ctx, settingsredis.SettingsChannel)
			defer sub.Close()
			if _, err := sub.Receive(ctx); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(config.TomlFilePath, []byte("[cellular]\napn = "+strconv.Quote(apn)+"\n[extension]\ncredential = \"private-fixture\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := svc.LoadSettingsFromTOML(); err != nil {
				t.Fatal(err)
			}
			for {
				msg, err := sub.ReceiveMessage(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if msg.Payload == "cellular.apn" {
					break
				}
			}
			if value, err := client.HGet(ctx, settingsredis.SettingsKey, "cellular.apn").Result(); err != nil || value != apn {
				t.Fatalf("hydrated APN mismatch: %v", err)
			}
			if value, err := client.HGet(ctx, settingsredis.SettingsKey, "extension.credential").Result(); err != nil || value != "private-fixture" {
				t.Fatal("custom setting changed")
			}
		})
	}
}

func TestAPNLivePersistenceAndEmptyRestorePreserveOtherSettings(t *testing.T) {
	svc, server := newLogPrivacyService(t, map[string]schema.Setting{"cellular.apn": {Type: "string"}})
	if err := os.WriteFile(config.TomlFilePath, []byte("[cellular]\napn = \"\"\nusername = \"custom-user\"\npassword = \"private-password\"\n[extension]\ncredential = \"private-fixture\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatal(err)
	}
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	defer client.Close()
	done := make(chan struct{})
	go func() { defer close(done); svc.WatchSettings() }()
	t.Cleanup(func() {
		svc.cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("settings watcher did not stop")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		counts, err := client.PubSubNumSub(ctx, settingsredis.SettingsChannel).Result()
		if err != nil {
			t.Fatal(err)
		}
		if counts[settingsredis.SettingsChannel] > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("settings subscriber unavailable")
		case <-time.After(time.Millisecond):
		}
	}
	for _, apn := range []string{"example.apn", ""} {
		if err := svc.redisClient.SetSettingField("cellular.apn", apn); err != nil {
			t.Fatal(err)
		}
		for {
			cfg, err := config.LoadFromFile()
			if err != nil {
				t.Fatal(err)
			}
			fields := cfg.ToRedisFields()
			if fields["cellular.apn"] == apn {
				info, err := os.Stat(config.TomlFilePath)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("APN persistence weakened settings permissions")
				}
				for key, want := range map[string]string{"cellular.username": "custom-user", "cellular.password": "private-password", "extension.credential": "private-fixture"} {
					if fields[key] != want {
						t.Errorf("APN update changed %s", key)
					}
				}
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("APN was not persisted")
			case <-time.After(time.Millisecond):
			}
		}
	}
}
