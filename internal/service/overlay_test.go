package service

import "testing"

func TestOverlayShouldPersist(t *testing.T) {
	cases := []struct {
		transient, overlaid, want bool
	}{
		{false, false, true},  // normal user setting -> persist
		{true, false, false},  // transient -> never persist
		{false, true, false},  // overlaid -> never persist (no-clobber)
		{true, true, false},
	}
	for _, c := range cases {
		if got := overlayShouldPersist(c.transient, c.overlaid); got != c.want {
			t.Errorf("overlayShouldPersist(%v,%v) = %v, want %v", c.transient, c.overlaid, got, c.want)
		}
	}
}

func TestServiceOverlayFields(t *testing.T) {
	f := serviceOverlayFields()
	want := map[string]string{
		"scooter.auto-standby-seconds": "0",
		"pm.hibernation-timer":         "0",
		"pm.default-state":             "run",
		"alarm.enabled":                "false",
		"scooter.usb0-policy":          "always-on",
		"dashboard.mode":               "debug",
		"scooter.handlebar-unlocked":   "true",
	}
	if len(f) != len(want) {
		t.Fatalf("len = %d, want %d", len(f), len(want))
	}
	for k, v := range want {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
}

func TestOverlayBaseForPersist(t *testing.T) {
	// inactive: no change
	s := map[string]string{"alarm.enabled": "false"}
	overlayBaseForPersist(s, false, map[string]capturedVal{"alarm.enabled": {value: "true", existed: true}})
	if s["alarm.enabled"] != "false" {
		t.Errorf("inactive should not change map, got %q", s["alarm.enabled"])
	}
	// active: overlaid key restored to base value for persistence
	s = map[string]string{"alarm.enabled": "false", "other": "x"}
	overlayBaseForPersist(s, true, map[string]capturedVal{"alarm.enabled": {value: "true", existed: true}})
	if s["alarm.enabled"] != "true" {
		t.Errorf("active should substitute base value, got %q", s["alarm.enabled"])
	}
	if s["other"] != "x" {
		t.Errorf("non-overlaid key must be untouched, got %q", s["other"])
	}
	// active, key did not exist pre-overlay: removed from persisted map
	s = map[string]string{"scooter.handlebar-unlocked": "true"}
	overlayBaseForPersist(s, true, map[string]capturedVal{"scooter.handlebar-unlocked": {existed: false}})
	if _, ok := s["scooter.handlebar-unlocked"]; ok {
		t.Errorf("non-existent base key should be deleted from persisted map")
	}
}

func TestHandleOverlaidEdit(t *testing.T) {
	s := &SettingsService{
		overlayActive: true,
		overlayBase: map[string]capturedVal{
			"alarm.enabled": {value: "true", existed: true, wasUserSet: true},
		},
	}
	// User sets alarm.enabled=false while overlay forces it false: same as
	// overlay value -> our own write, not a user edit.
	reassert, isEdit := s.handleOverlaidEdit("alarm.enabled", "false")
	if isEdit {
		t.Errorf("write matching overlay value should not be a user edit")
	}
	_ = reassert
	// User sets alarm.enabled=true (differs from overlay "false") -> user edit:
	// base updated, overlay value re-asserted.
	reassert, isEdit = s.handleOverlaidEdit("alarm.enabled", "true")
	if !isEdit {
		t.Fatal("differing write should be a user edit")
	}
	if reassert != "false" {
		t.Errorf("reassert = %q, want overlay value %q", reassert, "false")
	}
	if got := s.overlayBase["alarm.enabled"].value; got != "true" {
		t.Errorf("captured base not updated: %q", got)
	}
}
