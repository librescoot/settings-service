package service

import (
	"testing"

	"github.com/librescoot/settings-service/internal/schema"
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
		"updates.mdb.channel": "nightly", // legacy stale value, must be ignored
		"updates.dbc.channel": "nightly",
		"scooter.logserver":   "https://example",
	}
	fields := map[string]any{}
	userSet := map[string]struct{}{}

	dropped := applyTomlOverlay(toml, sch, fields, userSet)

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
	// No schema = nothing transient = legacy behavior preserved.
	toml := map[string]any{"updates.mdb.channel": "nightly"}
	fields := map[string]any{}
	userSet := map[string]struct{}{}

	dropped := applyTomlOverlay(toml, nil, fields, userSet)
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

func TestMarkUserSet(t *testing.T) {
	s := &SettingsService{userSetKeys: make(map[string]struct{})}

	s.markUserSet("alarm.enabled")
	if _, ok := s.userSetKeys["alarm.enabled"]; !ok {
		t.Errorf("expected alarm.enabled in userSetKeys after markUserSet")
	}

	// Idempotent
	s.markUserSet("alarm.enabled")
	if len(s.userSetKeys) != 1 {
		t.Errorf("expected 1 key, got %d", len(s.userSetKeys))
	}
}
