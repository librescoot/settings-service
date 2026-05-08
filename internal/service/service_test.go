package service

import (
	"testing"
)

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
