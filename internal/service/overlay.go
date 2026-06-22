package service

import "log"

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

// ApplyServiceOverlay captures the current base value of each overridden key,
// writes the overlay values to the live settings hash (publishing each so
// consumers react), persists the active flag, and publishes the status field.
// Overlay writes never reach /data/settings.toml because WatchSettings skips
// persistence for overlaid keys (see overlayShouldPersist / isOverlaid).
func (s *SettingsService) ApplyServiceOverlay() error {
	s.mu.Lock()
	if s.overlayActive {
		s.mu.Unlock()
		return nil
	}
	overlay := serviceOverlayFields()
	base := make(map[string]capturedVal, len(overlay))
	for k := range overlay {
		cur, existed, err := s.redisClient.GetSettingField(k)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		_, wasUserSet := s.userSetKeys[k]
		base[k] = capturedVal{value: cur, existed: existed, wasUserSet: wasUserSet}
	}
	s.overlayBase = base
	s.overlayActive = true
	s.mu.Unlock()

	if err := saveOverlayActive(true); err != nil {
		log.Printf("Failed to persist service overlay flag: %v", err)
	}
	for k, v := range overlay {
		if err := s.redisClient.SetSettingField(k, v); err != nil {
			log.Printf("Overlay apply: failed to set %s: %v", k, err)
		}
	}
	if err := s.redisClient.SetSettingField(overlayStatusField, "true"); err != nil {
		log.Printf("Overlay apply: failed to publish status: %v", err)
	}
	log.Printf("Service overlay applied (%d keys)", len(overlay))
	return nil
}

// ClearServiceOverlay restores each overridden key to its captured base value
// (re-establishing user-set membership), clears the active flag, and publishes
// the status field. Absent-at-capture keys are left as-is.
func (s *SettingsService) ClearServiceOverlay() error {
	s.mu.Lock()
	if !s.overlayActive {
		s.mu.Unlock()
		return nil
	}
	base := s.overlayBase
	s.overlayActive = false
	s.overlayBase = nil
	for k, c := range base {
		if c.wasUserSet {
			s.userSetKeys[k] = struct{}{}
		}
	}
	s.mu.Unlock()

	if err := saveOverlayActive(false); err != nil {
		log.Printf("Failed to clear service overlay flag: %v", err)
	}
	for k, c := range base {
		if !c.existed {
			continue
		}
		if err := s.redisClient.SetSettingField(k, c.value); err != nil {
			log.Printf("Overlay clear: failed to restore %s: %v", k, err)
		}
	}
	if err := s.redisClient.SetSettingField(overlayStatusField, "false"); err != nil {
		log.Printf("Overlay clear: failed to publish status: %v", err)
	}
	if err := s.SaveSettingsToTOML(); err != nil {
		log.Printf("Overlay clear: failed to persist restored base: %v", err)
	}
	log.Printf("Service overlay cleared (%d keys restored)", len(base))
	return nil
}
