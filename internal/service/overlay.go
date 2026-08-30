package service

import (
	"log"
	"sort"
)

// overlayStatusField is the UI-visible state of the persistent service overlay.
const overlayStatusField = "dashboard.service-mode-active"

type capturedVal struct {
	value      string
	existed    bool
	wasUserSet bool
}

// Neither runtime-only settings nor forced overlay values may reach TOML.
func overlayShouldPersist(transient, overlaid bool) bool {
	return !transient && !overlaid
}

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

// overlayBaseForPersist substitutes captured values so the overlay never
// clobbers the user's persisted configuration.
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

// Absent pre-overlay values return to their default, or leave the hash entirely.
func overlayRestorePlan(base map[string]capturedVal, defaults map[string]string) (map[string]string, []string) {
	set := make(map[string]string, len(base))
	var drop []string
	for k, c := range base {
		if c.existed {
			set[k] = c.value
			continue
		}
		if def, ok := defaults[k]; ok {
			set[k] = def
			continue
		}
		drop = append(drop, k)
	}
	sort.Strings(drop)
	return set, drop
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *SettingsService) isOverlaid(field string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.overlayActive {
		return false
	}
	_, ok := s.overlayBase[field]
	return ok
}

// ApplyServiceOverlay captures the base before forcing effective service-mode values.
func (s *SettingsService) ApplyServiceOverlay() error {
	overlay := serviceOverlayFields()

	type rawVal struct {
		value   string
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

// ClearServiceOverlay restores captured membership and values so no forced setting
// survives service mode (notably handlebar unlock and dashboard debug mode).
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
	var defaults map[string]string
	if s.schema != nil {
		defaults = s.schema.Defaults()
	}
	set, drop := overlayRestorePlan(base, defaults)
	for _, k := range sortedKeys(set) {
		if err := s.redisClient.SetSettingField(k, set[k]); err != nil {
			log.Printf("Overlay clear: failed to restore %s: %v", k, err)
		}
	}
	if err := s.redisClient.DeleteSettingsFields(drop); err != nil {
		log.Printf("Overlay clear: failed to drop %v: %v", drop, err)
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

// ReapplyOverlayOnBoot restores the persisted mode after Redis is rehydrated.
func (s *SettingsService) ReapplyOverlayOnBoot() {
	if loadOverlayActive() {
		log.Printf("Service overlay was active before reboot; re-applying")
		if err := s.ApplyServiceOverlay(); err != nil {
			log.Printf("Boot re-apply failed: %v", err)
		}
	}
}

// handleOverlaidEdit records a user change as the future base, then tells the
// watcher to reassert the active overlay. Its own matching writes are ignored.
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

func (s *SettingsService) currentFieldValue(field string) string {
	v, _, err := s.redisClient.GetSettingField(field)
	if err != nil {
		return ""
	}
	return v
}
