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
