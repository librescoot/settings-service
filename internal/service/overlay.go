package service

// overlayStatusField is the settings-hash field that publishes whether the
// service overlay is active, for UI consumption.
const overlayStatusField = "dashboard.service-mode-active"

// serviceOverlayFields returns the fixed set of setting overrides applied
// while service mode is active. Values are the canonical string forms stored
// in the Redis settings hash.
func serviceOverlayFields() map[string]string {
	return map[string]string{
		"scooter.auto-standby-seconds": "0",
		"pm.hibernation-timer":         "0",
		"pm.default-state":             "run",
		"alarm.enabled":                "false",
		"scooter.usb0-policy":          "always-on",
		"dashboard.mode":               "debug",
		"scooter.handlebar-unlocked":   "true",
	}
}
