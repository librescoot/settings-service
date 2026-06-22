package service

// overlayStatusField is the settings-hash field that publishes whether the
// service overlay is active, for UI consumption.
const overlayStatusField = "dashboard.service-mode-active"

// capturedVal records a setting's value before the overlay overrode it, so it
// can be restored verbatim on clear.
type capturedVal struct {
	value      string
	existed    bool
	wasUserSet bool
}

// overlayShouldPersist reports whether a changed settings field should be
// written to /data/settings.toml. Transient keys never persist; keys currently
// forced by the overlay never persist (no-clobber of the user's base config).
func overlayShouldPersist(transient, overlaid bool) bool {
	return !transient && !overlaid
}

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

// isOverlaid reports whether field is currently forced by the active overlay.
func (s *SettingsService) isOverlaid(field string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.overlayActive {
		return false
	}
	_, ok := s.overlayBase[field]
	return ok
}
