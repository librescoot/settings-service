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

// overlayBaseForPersist rewrites settings so overlaid keys carry their captured
// base value (or are removed if they did not exist pre-overlay) before the map
// is persisted to TOML. No-op when the overlay is inactive. Pure: mutates the
// passed map in place.
func overlayBaseForPersist(settings map[string]string, active bool, base map[string]capturedVal) {
	if !active {
		return
	}
	for k, c := range base {
		if c.existed {
			settings[k] = c.value
		} else {
			delete(settings, k)
		}
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
	overlay := serviceOverlayFields()

	// Capture current values without holding the lock — Redis I/O must not
	// block other goroutines that need mu (e.g. WatchSettings).
	type rawVal struct {
		value  string
		existed bool
	}
	captured := make(map[string]rawVal, len(overlay))
	for k := range overlay {
		cur, existed, err := s.redisClient.GetSettingField(k)
		if err != nil {
			return err
		}
		captured[k] = rawVal{value: cur, existed: existed}
	}

	s.mu.Lock()
	if s.overlayActive {
		s.mu.Unlock()
		return nil
	}
	base := make(map[string]capturedVal, len(overlay))
	for k, raw := range captured {
		_, wasUserSet := s.userSetKeys[k]
		base[k] = capturedVal{value: raw.value, existed: raw.existed, wasUserSet: wasUserSet}
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

// RunOverlayConsumer blocks on the settings:overlay list and dispatches
// apply/clear commands. Intended to run in its own goroutine.
func (s *SettingsService) RunOverlayConsumer() {
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		cmd, err := s.redisClient.BRPopOverlay()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			log.Printf("overlay consumer BRPop error: %v", err)
			continue
		}
		switch cmd {
		case "apply:service":
			if err := s.ApplyServiceOverlay(); err != nil {
				log.Printf("apply:service failed: %v", err)
			}
		case "clear:service":
			if err := s.ClearServiceOverlay(); err != nil {
				log.Printf("clear:service failed: %v", err)
			}
		default:
			log.Printf("overlay consumer: unknown command %q", cmd)
		}
	}
}

// ReapplyOverlayOnBoot re-applies the service overlay if it was active before a
// reboot. Call after the base settings are loaded into Redis.
func (s *SettingsService) ReapplyOverlayOnBoot() {
	if loadOverlayActive() {
		log.Printf("Service overlay was active before reboot; re-applying")
		if err := s.ApplyServiceOverlay(); err != nil {
			log.Printf("Boot re-apply failed: %v", err)
		}
	}
}

// handleOverlaidEdit reconciles a change to an overlaid key observed on the
// settings channel. If newValue matches the overlay's forced value it is our
// own write (isUserEdit=false). Otherwise it is a genuine user edit: the
// captured base is updated so it surfaces on clear, and reassert returns the
// overlay value the caller must re-write to keep the effective value overridden.
func (s *SettingsService) handleOverlaidEdit(field, newValue string) (reassert string, isUserEdit bool) {
	overlayVal := serviceOverlayFields()[field]
	if newValue == overlayVal {
		return "", false
	}
	s.mu.Lock()
	if c, ok := s.overlayBase[field]; ok {
		c.value = newValue
		c.existed = true
		c.wasUserSet = true
		s.overlayBase[field] = c
	}
	s.mu.Unlock()
	return overlayVal, true
}

// currentFieldValue reads the live settings-hash value of field (empty if absent).
func (s *SettingsService) currentFieldValue(field string) string {
	v, _, err := s.redisClient.GetSettingField(field)
	if err != nil {
		return ""
	}
	return v
}
