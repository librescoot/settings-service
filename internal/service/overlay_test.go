package service

import "testing"

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
