package service

import (
	"testing"

	"github.com/librescoot/settings-service/internal/schema"
)

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

func TestOverlayRestorePlan(t *testing.T) {
	base := map[string]capturedVal{
		"alarm.enabled":              {value: "true", existed: true},
		"dashboard.mode":             {existed: false},
		"scooter.handlebar-unlocked": {existed: false},
	}
	defaults := map[string]string{
		"dashboard.mode": "speedometer",
		"alarm.enabled":  "true",
	}

	set, drop := overlayRestorePlan(base, defaults)

	if set["alarm.enabled"] != "true" {
		t.Errorf("captured key should return to its captured value, got %q", set["alarm.enabled"])
	}
	// The whole point: a key with no pre-overlay value must not keep the
	// overlay value. With a schema default it goes back to that.
	if set["dashboard.mode"] != "speedometer" {
		t.Errorf("absent key should fall back to its schema default, got %q", set["dashboard.mode"])
	}
	if _, ok := set["scooter.handlebar-unlocked"]; ok {
		t.Errorf("absent key with no default must not be written back")
	}
	if len(drop) != 1 || drop[0] != "scooter.handlebar-unlocked" {
		t.Errorf("drop = %v, want [scooter.handlebar-unlocked]", drop)
	}
}

func TestOverlayRestorePlanEveryOverlayKeyIsRestorable(t *testing.T) {
	// Every overlaid key needs a way back even when it was absent at capture,
	// otherwise clearing service mode leaves it forced forever.
	s, err := schema.LoadFile("../../settings.schema.json")
	if err != nil {
		t.Skipf("schema not readable: %v", err)
	}
	defaults := s.Defaults()
	for k := range serviceOverlayFields() {
		if _, ok := defaults[k]; !ok {
			t.Errorf("%s has no schema default, so clearing the overlay can only drop it", k)
		}
	}
}
