package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/librescoot/settings-service/internal/config"
	"github.com/librescoot/settings-service/internal/redis"
	"github.com/librescoot/settings-service/internal/schema"
	goredis "github.com/redis/go-redis/v9"
)

func TestApplyTomlOverlay(t *testing.T) {
	const schemaJSON = `{
  "alarm.enabled":       {"type": "bool", "default": true},
  "updates.mdb.channel": {"type": "enum", "transient": true},
  "updates.dbc.channel": {"type": "enum", "transient": true}
}`
	sch, err := schema.Parse([]byte(schemaJSON))
	if err != nil {
		t.Fatalf("schema.Parse: %v", err)
	}

	toml := map[string]any{
		"alarm.enabled":       "true",
		"updates.mdb.channel": "nightly",
		"updates.dbc.channel": "nightly",
		"scooter.logserver":   "https://example",
	}
	fields := map[string]any{}
	userSet := map[string]struct{}{}

	dropped, invalid := applyTomlOverlay(toml, sch, fields, userSet)
	if len(invalid) != 0 {
		t.Errorf("invalid = %v, want none", invalid)
	}

	if len(dropped) != 2 {
		t.Errorf("dropped count = %d, want 2 (got %v)", len(dropped), dropped)
	}
	wantDropped := map[string]struct{}{
		"updates.mdb.channel": {},
		"updates.dbc.channel": {},
	}
	for _, k := range dropped {
		if _, ok := wantDropped[k]; !ok {
			t.Errorf("unexpected dropped key %q", k)
		}
	}

	for _, k := range []string{"updates.mdb.channel", "updates.dbc.channel"} {
		if _, ok := fields[k]; ok {
			t.Errorf("transient key %q should not be loaded into fields", k)
		}
		if _, ok := userSet[k]; ok {
			t.Errorf("transient key %q should not be marked user-set", k)
		}
	}

	for _, k := range []string{"alarm.enabled", "scooter.logserver"} {
		if _, ok := fields[k]; !ok {
			t.Errorf("persistent key %q should be loaded into fields", k)
		}
		if _, ok := userSet[k]; !ok {
			t.Errorf("persistent key %q should be marked user-set", k)
		}
	}
}

func TestApplyTomlOverlay_NilSchema(t *testing.T) {

	toml := map[string]any{"updates.mdb.channel": "nightly"}
	fields := map[string]any{}
	userSet := map[string]struct{}{}

	dropped, invalid := applyTomlOverlay(toml, nil, fields, userSet)
	if len(invalid) != 0 {
		t.Errorf("nil schema invalid = %v, want none", invalid)
	}
	if len(dropped) != 0 {
		t.Errorf("nil schema should drop nothing, got %v", dropped)
	}

	if fields["updates.mdb.channel"] != "nightly" {
		t.Error("nil schema should preserve legacy persist-everything behavior")
	}
	if _, ok := userSet["updates.mdb.channel"]; !ok {
		t.Error("nil schema should mark every toml key user-set")
	}
}

func TestTransientKeys(t *testing.T) {
	const schemaJSON = `{
  "alarm.enabled":       {"type": "bool"},
  "updates.mdb.channel": {"type": "enum", "transient": true},
  "updates.dbc.channel": {"type": "enum", "transient": true}
}`
	sch, err := schema.Parse([]byte(schemaJSON))
	if err != nil {
		t.Fatalf("schema.Parse: %v", err)
	}

	keys := transientKeys(sch)
	if len(keys) != 2 {
		t.Fatalf("len = %d, want 2 (got %v)", len(keys), keys)
	}
	want := map[string]bool{"updates.mdb.channel": true, "updates.dbc.channel": true}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected key %q", k)
		}
	}

	if got := transientKeys(nil); got != nil {
		t.Errorf("nil schema = %v, want nil", got)
	}
}

func TestEqualStringMaps(t *testing.T) {
	if !equalStringMaps(map[string]string{"a": "1"}, map[string]string{"a": "1"}) {
		t.Fatal("equal maps should compare equal")
	}
	if equalStringMaps(map[string]string{"a": "1"}, map[string]string{"a": "2"}) {
		t.Fatal("different values should not compare equal")
	}
	if equalStringMaps(map[string]string{"a": "1"}, map[string]string{"a": "1", "b": "2"}) {
		t.Fatal("different keys should not compare equal")
	}
	if equalStringMaps(map[string]string{"a": ""}, map[string]string{"b": ""}) {
		t.Fatal("different empty-valued keys should not compare equal")
	}
	if !equalStringMaps(map[string]string{}, nil) {
		t.Fatal("empty and nil maps should compare equal")
	}
}

func TestCloneStringMap(t *testing.T) {
	original := map[string]string{"alarm.enabled": "true"}
	clone := cloneStringMap(original)
	clone["alarm.enabled"] = "false"
	if original["alarm.enabled"] != "true" {
		t.Fatal("clone must not share storage with original")
	}
}

func TestFilterUserSet(t *testing.T) {
	tests := []struct {
		name     string
		settings map[string]string
		userSet  map[string]struct{}
		want     map[string]string
	}{
		{
			name: "only user-set keys are kept",
			settings: map[string]string{
				"updates.mdb.channel": "nightly",
				"updates.mdb.method":  "delta",
				"alarm.enabled":       "true",
			},
			userSet: map[string]struct{}{
				"alarm.enabled": {},
			},
			want: map[string]string{
				"alarm.enabled": "true",
			},
		},
		{
			name: "empty user-set produces empty output",
			settings: map[string]string{
				"updates.mdb.channel": "nightly",
			},
			userSet: map[string]struct{}{},
			want:    map[string]string{},
		},
		{
			name: "user-set key missing from Redis is dropped silently",
			settings: map[string]string{
				"alarm.enabled": "true",
			},
			userSet: map[string]struct{}{
				"alarm.enabled": {},
				"vanished.key":  {},
			},
			want: map[string]string{
				"alarm.enabled": "true",
			},
		},
		{
			name: "all keys user-set keeps everything",
			settings: map[string]string{
				"a": "1",
				"b": "2",
			},
			userSet: map[string]struct{}{
				"a": {},
				"b": {},
			},
			want: map[string]string{
				"a": "1",
				"b": "2",
			},
		},
		{
			name: "saved location notification keeps the complete record",
			settings: map[string]string{
				"dashboard.saved-locations.2.label":     "Home",
				"dashboard.saved-locations.2.latitude":  "52.52",
				"dashboard.saved-locations.2.longitude": "13.405",
				"dashboard.saved-locations.3.label":     "Work",
				"dashboard.theme":                       "dark",
			},
			userSet: map[string]struct{}{
				"dashboard.saved-locations.2": {},
			},
			want: map[string]string{
				"dashboard.saved-locations.2.label":     "Home",
				"dashboard.saved-locations.2.latitude":  "52.52",
				"dashboard.saved-locations.2.longitude": "13.405",
			},
		},
		{
			name: "recent destination notification keeps the complete record",
			settings: map[string]string{
				"dashboard.recent-destinations.0.label":   "Park",
				"dashboard.recent-destinations.0.used-at": "2026-09-06T10:00:00Z",
			},
			userSet: map[string]struct{}{
				"dashboard.recent-destinations.0": {},
			},
			want: map[string]string{
				"dashboard.recent-destinations.0.label":   "Park",
				"dashboard.recent-destinations.0.used-at": "2026-09-06T10:00:00Z",
			},
		},
		{
			name:     "saved location deletion drops the persisted record",
			settings: map[string]string{},
			userSet: map[string]struct{}{
				"dashboard.saved-locations.1":           {},
				"dashboard.saved-locations.1.label":     {},
				"dashboard.saved-locations.1.latitude":  {},
				"dashboard.saved-locations.1.longitude": {},
			},
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterUserSet(tt.settings, tt.userSet)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (got %v)", len(got), len(tt.want), got)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("got[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestWatchSettingsPersistsTripSettings(t *testing.T) {
	server := miniredis.RunT(t)
	originalTomlPath := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = originalTomlPath })

	svc, err := New(server.Addr(), "")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	watchDone := make(chan struct{})
	go func() {
		svc.WatchSettings()
		close(watchDone)
	}()
	t.Cleanup(func() {
		svc.Close()
		select {
		case <-watchDone:
		case <-time.After(time.Second):
			t.Error("WatchSettings did not stop")
		}
	})

	deadline := time.Now().Add(time.Second)
	for server.Publish(redis.SettingsChannel, "") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("WatchSettings did not subscribe")
		}
		time.Sleep(time.Millisecond)
	}

	server.HSet(redis.SettingsKey, "trip.counter-reset", "manual")
	server.Publish(redis.SettingsChannel, "trip.counter-reset")
	server.HSet(redis.SettingsKey, "trip.expunge", "age:365d")
	server.Publish(redis.SettingsChannel, "trip.expunge")

	deadline = time.Now().Add(time.Second)
	for {
		cfg, err := config.LoadFromFile()
		if err == nil {
			fields := cfg.ToRedisFields()
			if fields["trip.counter-reset"] == "manual" && fields["trip.expunge"] == "age:365d" {
				return
			}
		}
		if time.Now().After(deadline) {
			if os.IsNotExist(err) {
				t.Fatal("trip settings were not persisted")
			}
			t.Fatalf("trip settings were not persisted, config error = %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLoadSettingsFromTOMLRepairsInvalidTripExpunge(t *testing.T) {
	server := miniredis.RunT(t)
	originalTomlPath := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = originalTomlPath })

	serviceSchemaPath := filepath.Join("..", "..", "settings.schema.json")
	svc, err := New(server.Addr(), serviceSchemaPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(svc.Close)

	if err := os.WriteFile(config.TomlFilePath, []byte("[trip]\nexpunge = \"age:1d\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatalf("LoadSettingsFromTOML() valid error: %v", err)
	}
	if err := os.WriteFile(config.TomlFilePath, []byte("[trip]\nexpunge = \"count:01\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatalf("LoadSettingsFromTOML() invalid error: %v", err)
	}

	value, exists, err := svc.redisClient.GetSettingField("trip.expunge")
	if err != nil || !exists || value != "age:1d" {
		t.Fatalf("hydrated trip.expunge = %q, exists=%v, err=%v; want restored age:1d", value, exists, err)
	}
	cfg, err := config.LoadFromFile()
	if err != nil {
		t.Fatalf("LoadFromFile() error: %v", err)
	}
	if got := cfg.ToRedisFields()["trip.expunge"]; got != "age:1d" {
		t.Errorf("repaired TOML trip.expunge = %v, want age:1d", got)
	}
}

func TestLoadSettingsFromTOMLUsesSafeTripExpungeFallbackAndDefault(t *testing.T) {
	server := miniredis.RunT(t)
	originalTomlPath := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = originalTomlPath })

	serviceSchemaPath := filepath.Join("..", "..", "settings.schema.json")
	svc, err := New(server.Addr(), serviceSchemaPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(svc.Close)
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatalf("LoadSettingsFromTOML() absent error: %v", err)
	}
	value, _, err := svc.redisClient.GetSettingField("trip.expunge")
	if err != nil || value != "age:365d" {
		t.Fatalf("absent TOML trip.expunge = %q, err=%v; want schema default age:365d", value, err)
	}

	if err := os.WriteFile(config.TomlFilePath, []byte("[trip]\nexpunge = \"age:0\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatalf("LoadSettingsFromTOML() invalid error: %v", err)
	}
	value, _, err = svc.redisClient.GetSettingField("trip.expunge")
	if err != nil || value != "never" {
		t.Fatalf("invalid TOML trip.expunge = %q, err=%v; want safe fallback never", value, err)
	}
}

func TestWatchSettingsRepairsInvalidTripExpungeOnceAndSurvivesReload(t *testing.T) {
	server := miniredis.RunT(t)
	originalTomlPath := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = originalTomlPath })

	serviceSchemaPath := filepath.Join("..", "..", "settings.schema.json")
	svc, err := New(server.Addr(), serviceSchemaPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatalf("LoadSettingsFromTOML() error: %v", err)
	}
	watchDone := make(chan struct{})
	go func() {
		svc.WatchSettings()
		close(watchDone)
	}()
	closed := false
	t.Cleanup(func() {
		if !closed {
			svc.Close()
		}
		select {
		case <-watchDone:
		case <-time.After(time.Second):
			t.Error("WatchSettings did not stop")
		}
	})
	waitForSettingsSubscription(t, server)

	server.HSet(redis.SettingsKey, "trip.expunge", "never")
	server.Publish(redis.SettingsChannel, "trip.expunge")
	waitForTripExpunge(t, svc, "never")

	observer := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = observer.Close() })
	pubsub := observer.Subscribe(context.Background(), redis.SettingsChannel)
	t.Cleanup(func() { _ = pubsub.Close() })
	if _, err := pubsub.ReceiveTimeout(context.Background(), time.Second); err != nil {
		t.Fatalf("observer subscription error: %v", err)
	}
	events := pubsub.Channel()

	server.HSet(redis.SettingsKey, "trip.expunge", "age:0")
	server.Publish(redis.SettingsChannel, "trip.expunge")
	waitForTripExpunge(t, svc, "never")

	for i := 0; i < 2; i++ {
		select {
		case msg := <-events:
			if msg.Payload != "trip.expunge" {
				t.Fatalf("notification %d payload = %q, want trip.expunge", i, msg.Payload)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing trip.expunge notification %d", i+1)
		}
	}
	select {
	case msg := <-events:
		t.Fatalf("unexpected extra notification %q; repair loop did not converge", msg.Payload)
	case <-time.After(100 * time.Millisecond):
	}

	cfg, err := config.LoadFromFile()
	if err != nil {
		t.Fatalf("LoadFromFile() error: %v", err)
	}
	if got := cfg.ToRedisFields()["trip.expunge"]; got != "never" {
		t.Errorf("persisted trip.expunge = %v, want never", got)
	}

	svc.Close()
	closed = true
	select {
	case <-watchDone:
	case <-time.After(time.Second):
		t.Fatal("WatchSettings did not stop for reload")
	}
	reloaded, err := New(server.Addr(), serviceSchemaPath)
	if err != nil {
		t.Fatalf("New() after reload error: %v", err)
	}
	t.Cleanup(reloaded.Close)
	if err := reloaded.LoadSettingsFromTOML(); err != nil {
		t.Fatalf("LoadSettingsFromTOML() after reload error: %v", err)
	}
	waitForTripExpunge(t, reloaded, "never")
}

func TestLoadSettingsFromTOMLRepairsInvalidTripCounterReset(t *testing.T) {
	server := miniredis.RunT(t)
	originalTomlPath := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = originalTomlPath })

	if err := os.WriteFile(config.TomlFilePath, []byte("[trip]\ncounter-reset = \"sometimes\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	svc, err := New(server.Addr(), filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(svc.Close)
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatalf("LoadSettingsFromTOML() error: %v", err)
	}
	value, exists, err := svc.redisClient.GetSettingField("trip.counter-reset")
	if err != nil || !exists || value != "ride" {
		t.Fatalf("trip.counter-reset = %q, exists=%v, err=%v; want ride", value, exists, err)
	}
	cfg, err := config.LoadFromFile()
	if err != nil {
		t.Fatalf("LoadFromFile() error: %v", err)
	}
	if got := cfg.ToRedisFields()["trip.counter-reset"]; got != "ride" {
		t.Errorf("repaired TOML trip.counter-reset = %v, want ride", got)
	}
}

func TestWatchSettingsRestoresLastValidTripCounterReset(t *testing.T) {
	server := miniredis.RunT(t)
	originalTomlPath := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = originalTomlPath })

	svc, err := New(server.Addr(), filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatalf("LoadSettingsFromTOML() error: %v", err)
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

	server.HSet(redis.SettingsKey, "trip.counter-reset", "manual")
	server.Publish(redis.SettingsChannel, "trip.counter-reset")
	waitForPersistedSetting(t, svc, "trip.counter-reset", "manual")
	server.HSet(redis.SettingsKey, "trip.counter-reset", "sometimes")
	server.Publish(redis.SettingsChannel, "trip.counter-reset")
	waitForPersistedSetting(t, svc, "trip.counter-reset", "manual")
}

func waitForPersistedSetting(t *testing.T, svc *SettingsService, key, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		value, exists, err := svc.redisClient.GetSettingField(key)
		if err == nil && exists && value == want {
			cfg, loadErr := config.LoadFromFile()
			if loadErr == nil && cfg.ToRedisFields()[key] == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not converge to %q", key, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForSettingsSubscription(t *testing.T, server *miniredis.Miniredis) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for server.Publish(redis.SettingsChannel, "") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("WatchSettings did not subscribe")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForTripExpunge(t *testing.T, svc *SettingsService, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		value, exists, err := svc.redisClient.GetSettingField("trip.expunge")
		if err == nil && exists && value == want {
			cfg, loadErr := config.LoadFromFile()
			if loadErr == nil && cfg.ToRedisFields()["trip.expunge"] == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("trip.expunge did not converge to %q", want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNewRejectsUnavailableConfiguredSchema(t *testing.T) {
	server := miniredis.RunT(t)

	svc, err := New(server.Addr(), filepath.Join(t.TempDir(), "missing.schema.json"))
	if err == nil {
		if svc != nil {
			svc.Close()
		}
		t.Fatal("New() accepted an unavailable configured schema")
	}
	if svc != nil {
		t.Fatalf("New() service = %#v, want nil on schema failure", svc)
	}
}

func TestLoadSettingsFromTOMLFailureLeavesRetentionPolicyUntouched(t *testing.T) {
	server := miniredis.RunT(t)
	originalTomlPath := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = originalTomlPath })
	if err := os.WriteFile(config.TomlFilePath, []byte("[trip\nexpunge = \"age:365d\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	svc, err := New(server.Addr(), filepath.Join("..", "..", "settings.schema.json"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(svc.Close)
	server.HSet(redis.SettingsKey, "trip.expunge", "never")
	server.HSet(redis.SettingsKey, "trip.retention-sentinel", "preserve-history")

	if err := svc.LoadSettingsFromTOML(); err == nil {
		t.Fatal("LoadSettingsFromTOML() succeeded with malformed TOML")
	}
	settings, err := svc.redisClient.GetAllSettings()
	if err != nil {
		t.Fatalf("GetAllSettings() error: %v", err)
	}
	if got := settings["trip.expunge"]; got != "never" {
		t.Errorf("trip.expunge = %q, want existing safe policy never", got)
	}
	if got := settings["trip.retention-sentinel"]; got != "preserve-history" {
		t.Errorf("retention sentinel = %q, want existing value", got)
	}
}

func TestMarkUserSet(t *testing.T) {
	s := &SettingsService{userSetKeys: make(map[string]struct{})}

	s.markUserSet("alarm.enabled")
	if _, ok := s.userSetKeys["alarm.enabled"]; !ok {
		t.Errorf("expected alarm.enabled in userSetKeys after markUserSet")
	}

	s.markUserSet("alarm.enabled")
	if len(s.userSetKeys) != 1 {
		t.Errorf("expected 1 key, got %d", len(s.userSetKeys))
	}
}
