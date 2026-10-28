package service

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/librescoot/settings-service/internal/config"
	"github.com/librescoot/settings-service/internal/redis"
	"github.com/librescoot/settings-service/internal/schema"
)

type settingsLogCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *settingsLogCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *settingsLogCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func captureSettingsLogs(t *testing.T) *settingsLogCapture {
	t.Helper()
	capture := &settingsLogCapture{}
	previous := log.Writer()
	log.SetOutput(capture)
	t.Cleanup(func() { log.SetOutput(previous) })
	return capture
}

func newLogPrivacyService(t *testing.T, settings map[string]schema.Setting) (*SettingsService, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	previous := config.TomlFilePath
	config.TomlFilePath = filepath.Join(t.TempDir(), "settings.toml")
	t.Cleanup(func() { config.TomlFilePath = previous })
	svc, err := New(server.Addr(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	svc.schema = &schema.Schema{Settings: settings}
	return svc, server
}

func assertNoSettingValues(t *testing.T, output string, values ...string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(output, value) {
			t.Errorf("setting value %q exposed in diagnostic output: %s", value, output)
		}
	}
}

func TestSettingsLogsSaveOmitsRedisValues(t *testing.T) {
	capture := captureSettingsLogs(t)
	svc, server := newLogPrivacyService(t, nil)
	values := map[string]string{
		"cellular.apn":         "apn-log-sentinel",
		"alarm.pin":            "91827364",
		"cellular.password":    "password-log-sentinel",
		"extension.credential": "extension-log-sentinel",
	}
	for key, value := range values {
		server.HSet(redis.SettingsKey, key, value)
		svc.markUserSet(key)
	}
	server.HSet(redis.SettingsKey, "dashboard.session-token", "redis-only-log-sentinel")
	if err := svc.SaveSettingsToTOML(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromFile()
	if err != nil {
		t.Fatal(err)
	}
	fields := cfg.ToRedisFields()
	for key, value := range values {
		if fields[key] != value {
			t.Errorf("persisted %s changed", key)
		}
		assertNoSettingValues(t, capture.String(), value)
	}
	if _, ok := fields["dashboard.session-token"]; ok {
		t.Error("Redis-only setting was persisted")
	}
	assertNoSettingValues(t, capture.String(), "redis-only-log-sentinel")
	for _, want := range []string{"Saved 4 settings", "extension.credential"} {
		if !strings.Contains(capture.String(), want) {
			t.Errorf("missing diagnostic %q", want)
		}
	}
}

func TestSettingsLogsTypedViolationsOmitValues(t *testing.T) {
	minimum, maximum := 999999.0, 10.0
	cases := []struct {
		name    string
		setting schema.Setting
		value   string
		derived string
	}{
		{"bool", schema.Setting{Type: "bool"}, "bool-log-sentinel", ""},
		{"int", schema.Setting{Type: "int"}, "int-log-sentinel", ""},
		{"float", schema.Setting{Type: "float"}, "float-log-sentinel", ""},
		{"duration", schema.Setting{Type: "duration"}, "duration-log-sentinel", ""},
		{"minimum", schema.Setting{Type: "int", Min: &minimum}, "123457", ""},
		{"maximum", schema.Setting{Type: "float", Max: &maximum}, "123456.75", ""},
		{"duration bound", schema.Setting{Type: "duration", Max: &maximum}, "937h", "3.3732e+06"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := captureSettingsLogs(t)
			const key = "dashboard.private-test"
			svc, server := newLogPrivacyService(t, map[string]schema.Setting{key: tc.setting})
			if svc.schema.ValidateTyped(key, tc.value) == nil {
				t.Fatal("fixture must violate the declared type or bounds")
			}
			server.HSet(redis.SettingsKey, key, tc.value)
			svc.markUserSet(key)
			svc.logTypedViolation(key)
			if err := svc.SaveSettingsToTOML(); err != nil {
				t.Fatal(err)
			}
			fields := map[string]any{}
			_, invalid := applyTomlOverlay(map[string]any{key: tc.value}, svc.schema, fields, map[string]struct{}{})
			if len(invalid) != 0 || fields[key] != tc.value || svc.lastPersisted[key] != tc.value {
				t.Fatal("log-only validation changed or rejected the value")
			}
			output := capture.String()
			if strings.Count(output, "Schema type violation for "+key) != 3 {
				t.Fatalf("expected live, save and load diagnostics: %s", output)
			}
			assertNoSettingValues(t, output, tc.value)
			if tc.derived != "" {
				assertNoSettingValues(t, output, tc.derived)
			}
		})
	}
}

func TestSettingsLogsRejectedValuesOmitValidatorDetails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setting schema.Setting
		value   string
	}{
		{"enum", schema.Setting{Values: []schema.EnumValue{{Value: "allowed"}}}, "rejected-log-sentinel"},
		{"format", schema.Setting{Format: schema.TripExpungeFormat}, "rejected-log-sentinel:42"},
		{"shortcut", schema.Setting{Format: schema.ShortcutItemsFormat}, `["rejected-log-sentinel"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := captureSettingsLogs(t)
			const key = "dashboard.private-test"
			svc, server := newLogPrivacyService(t, map[string]schema.Setting{key: tc.setting})
			server.HSet(redis.SettingsKey, key, tc.value)
			svc.markUserSet(key)
			err := svc.SaveSettingsToTOML()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected key-specific validation error, got %v", err)
			}
			log.Printf("Error saving settings to TOML: %v", err)
			if _, err := os.Stat(config.TomlFilePath); !os.IsNotExist(err) {
				t.Fatalf("invalid setting wrote a TOML file: %v", err)
			}
			fields := map[string]any{}
			_, invalid := applyTomlOverlay(map[string]any{key: tc.value}, svc.schema, fields, map[string]struct{}{})
			if invalid[key] != tc.value || len(fields) != 0 {
				t.Fatal("invalid TOML value was not rejected")
			}
			assertNoSettingValues(t, capture.String()+err.Error(), "rejected-log-sentinel")
		})
	}
}

func TestSettingsLogsReconciliationOmitsValuesAndFallbacks(t *testing.T) {
	for _, fallback := range []string{"", "fallback-log-sentinel"} {
		t.Run(fmt.Sprintf("fallback=%t", fallback != ""), func(t *testing.T) {
			capture := captureSettingsLogs(t)
			const key = "dashboard.private-test"
			svc, server := newLogPrivacyService(t, map[string]schema.Setting{
				key: {Values: []schema.EnumValue{{Value: "fallback-log-sentinel"}}},
			})
			svc.lastPersisted = map[string]string{key: fallback}
			server.HSet(redis.SettingsKey, key, "invalid-log-sentinel")
			if svc.reconcileValidatedSetting(key) {
				t.Fatal("invalid live setting was accepted")
			}
			want := fallback
			if want == "" {
				want = "invalid-log-sentinel"
			}
			assertSettingValue(t, svc, key, want)
			if !strings.Contains(capture.String(), "Rejecting invalid live "+key) {
				t.Fatal("missing key-specific rejection diagnostic")
			}
			assertNoSettingValues(t, capture.String(), "invalid-log-sentinel", "fallback-log-sentinel")
		})
	}
}

func TestSettingsLogsTOMLRepairOmitsValuesAndFallbacks(t *testing.T) {
	capture := captureSettingsLogs(t)
	const key = "dashboard.private-test"
	svc, _ := newLogPrivacyService(t, map[string]schema.Setting{
		key: {Default: "fallback-log-sentinel", Values: []schema.EnumValue{{Value: "fallback-log-sentinel"}}},
	})
	if err := os.WriteFile(config.TomlFilePath, []byte("[dashboard]\nprivate-test = \"invalid-log-sentinel\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.LoadSettingsFromTOML(); err != nil {
		t.Fatal(err)
	}
	assertSettingValue(t, svc, key, "fallback-log-sentinel")
	if svc.lastPersisted[key] != "fallback-log-sentinel" {
		t.Fatal("fallback was not persisted")
	}
	if !strings.Contains(capture.String(), "restoring") {
		t.Fatal("missing restoration diagnostic")
	}
	assertNoSettingValues(t, capture.String(), "invalid-log-sentinel", "fallback-log-sentinel")
}
