package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/librescoot/redis-ipc"
	"github.com/librescoot/settings-service/internal/config"
	"github.com/librescoot/settings-service/internal/destination"
	"github.com/librescoot/settings-service/internal/journalupload"
	"github.com/librescoot/settings-service/internal/network"
	"github.com/librescoot/settings-service/internal/redis"
	"github.com/librescoot/settings-service/internal/routeplan"
	"github.com/librescoot/settings-service/internal/schema"
)

type SettingsService struct {
	redisClient *redis.Client
	schema      *schema.Schema
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex

	// Destination records are managed through the RPC channel, never by
	// callers writing fields directly.
	ipcClient  *redis_ipc.Client
	destServer *destination.Server
	planServer *routeplan.Server

	// Only TOML-loaded and runtime user edits persist; schema defaults remain in Redis.
	userSetKeys map[string]struct{}

	// Overlay values are effective only and must never replace the captured user base.
	overlayActive bool
	overlayBase   map[string]capturedVal

	lastPersisted map[string]string

	osReleasePath                 string
	localReleaseChannel           string
	generatedChannelDefaults      map[string]string
	pendingGeneratedNotifications map[string]string
}

func New(redisAddr, schemaPath string) (*SettingsService, error) {
	ctx, cancel := context.WithCancel(context.Background())

	redisClient, err := redis.NewClient(ctx, redisAddr)
	if err != nil {
		cancel()
		return nil, err
	}

	var s *schema.Schema
	if schemaPath != "" {
		s, err = schema.LoadFile(schemaPath)
		if err != nil {
			redisClient.Close()
			cancel()
			return nil, fmt.Errorf("failed to load configured schema: %w", err)
		}
		log.Printf("Loaded schema with %d settings", len(s.Settings))
	}

	host, port, err := destination.ParseAddr(redisAddr)
	if err != nil {
		redisClient.Close()
		cancel()
		return nil, err
	}
	ipcClient, err := redis_ipc.New(redis_ipc.WithAddress(host), redis_ipc.WithPort(port))
	if err != nil {
		redisClient.Close()
		cancel()
		return nil, fmt.Errorf("failed to connect RPC client: %w", err)
	}
	destServer := destination.NewServer(ipcClient)
	destServer.Start()
	planServer := routeplan.NewServer(ipcClient, strings.TrimSuffix(config.TomlFilePath, ".toml")+"-route-plan.json")

	return &SettingsService{
		redisClient:                   redisClient,
		ipcClient:                     ipcClient,
		destServer:                    destServer,
		planServer:                    planServer,
		schema:                        s,
		ctx:                           ctx,
		cancel:                        cancel,
		userSetKeys:                   make(map[string]struct{}),
		osReleasePath:                 defaultOSReleasePath,
		generatedChannelDefaults:      make(map[string]string),
		pendingGeneratedNotifications: make(map[string]string),
	}, nil
}

// LoadSettingsFromTOML hydrates defaults and user settings while removing transient
// TOML entries, which are runtime-only even across a service restart.
func (s *SettingsService) LoadSettingsFromTOML() error {
	s.mu.Lock()
	rewriteToml := false
	defer func() {
		s.mu.Unlock()
		if rewriteToml {
			if err := s.SaveSettingsToTOML(); err != nil {
				log.Printf("Error rewriting toml after dropping transient keys: %v", err)
			}
		}
	}()

	fields := make(map[string]any)
	if s.schema != nil {
		for k, v := range s.schema.Defaults() {
			fields[k] = v
		}
	}

	userSet := make(map[string]struct{})
	var droppedTransient []string
	invalid := make(map[string]string)
	cfg, err := config.LoadFromFile()
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		log.Printf("No %s found, using schema defaults only", config.TomlFilePath)
	} else {
		migrated, migrateErr := migrateMilestoneSettings(cfg)
		if migrateErr != nil {
			return migrateErr
		}
		if migrated {
			if err := config.SaveToFile(cfg); err != nil {
				return fmt.Errorf("save milestone settings migration: %w", err)
			}
		}
		droppedTransient, invalid = applyTomlOverlay(cfg.ToRedisFields(), s.schema, fields, userSet)
	}
	for key, invalidValue := range invalid {
		fallback := s.lastPersisted[key]
		if fallback == "" {
			if key == "trip.expunge" {
				fallback = "never"
			} else if defaultValue, ok := s.schema.DefaultValue(key); ok {
				fallback = defaultValue
			}
		}
		if fallback == "" {
			continue
		}
		log.Printf("Ignoring invalid %s value %q from TOML and restoring %q", key, invalidValue, fallback)
		fields[key] = fallback
		userSet[key] = struct{}{}
	}
	s.applyInitialChannelDefaults(fields, userSet)
	rewriteToml = len(droppedTransient) > 0 || len(invalid) > 0
	if destination.HealUUIDs(fields, userSet) {
		rewriteToml = true
	}
	s.userSetKeys = userSet
	if rewriteToml {
		// Force the repaired TOML to be written even if it matches a prior snapshot.
		s.lastPersisted = nil
	} else {
		s.lastPersisted = persistedFields(fields, userSet)
	}

	if err := s.redisClient.ReplaceSettings(fields); err != nil {
		return fmt.Errorf("failed to write settings to Redis: %w", err)
	}
	if err := s.redisClient.DeleteSettingsFields([]string{"dashboard.milestone-celebrations"}); err != nil {
		return fmt.Errorf("remove legacy milestone setting from Redis: %w", err)
	}
	if err := s.planServer.Load(fields); err != nil {
		return fmt.Errorf("load route plan: %w", err)
	}
	s.planServer.Start()

	// Redis survives a settings-service restart, so remove transient keys not
	// freshly hydrated from a schema default.
	var stale []string
	for _, k := range transientKeys(s.schema) {
		if _, ok := fields[k]; !ok {
			stale = append(stale, k)
		}
	}
	if err := s.redisClient.DeleteSettingsFields(stale); err != nil {
		log.Printf("Error clearing transient keys from Redis: %v", err)
	}

	defaultCount := 0
	if s.schema != nil {
		defaultCount = len(s.schema.Defaults())
	}
	log.Printf("Loaded %d settings to Redis (%d from schema defaults)", len(fields), defaultCount)

	if s.schema != nil {
		if err := s.redisClient.SetKey(redis.SchemaKey, string(s.schema.Raw)); err != nil {
			return fmt.Errorf("failed to publish schema to Redis: %w", err)
		}
		log.Printf("Published schema to Redis key %q", redis.SchemaKey)
	}

	apnStr := ""
	if v, ok := fields["cellular.apn"]; ok && v != nil {
		apnStr = fmt.Sprintf("%v", v)
	}
	if apnStr != "" {
		go func() {
			currentAPN, err := network.GetCurrentAPN()
			if err != nil {
				log.Printf("Error reading current APN: %v", err)
				return
			}
			if currentAPN != apnStr {
				log.Printf("APN mismatch detected: NetworkManager has '%s', settings have '%s'", currentAPN, apnStr)
				if err := network.UpdateAPN(apnStr); err != nil {
					log.Printf("Error updating NetworkManager APN on startup: %v", err)
				}
			} else {
				log.Printf("APN is already synchronized: %s", apnStr)
			}
		}()
	}

	logserver := ""
	if v, ok := fields["scooter.logserver"]; ok && v != nil {
		logserver = fmt.Sprintf("%v", v)
	}
	go func() {
		if err := journalupload.ApplyLogServer(logserver); err != nil {
			log.Printf("Error applying log server on startup: %v", err)
		}
	}()

	return nil
}

// SaveSettingsToTOML persists only user-set, non-overlay values; defaults and
// transient values deliberately stay out of /data/settings.toml.
func (s *SettingsService) SaveSettingsToTOML() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	settings, err := s.redisClient.GetAllSettings()
	if err != nil {
		return fmt.Errorf("failed to get settings from Redis: %w", err)
	}

	overlayBaseForPersist(settings, s.overlayActive, s.overlayBase)

	persisted := filterUserSet(settings, s.userSetKeys)
	for key, value := range persisted {
		if err := s.schema.ValidateValue(key, value); err != nil {
			return fmt.Errorf("refusing to persist invalid %s value: %w", key, err)
		}
		if err := s.schema.ValidateTyped(key, value); err != nil {
			log.Printf("Schema type violation for %s value %q while persisting (log-only): %v", key, value, err)
		}
	}
	if equalStringMaps(persisted, s.lastPersisted) {
		return nil
	}

	log.Printf("Retrieved %d settings from Redis", len(settings))
	for k, v := range settings {
		log.Printf("  %s = %s", k, v)
	}

	for field := range settings {
		if !strings.HasPrefix(field, "scooter.") && !strings.HasPrefix(field, "cellular.") && !strings.HasPrefix(field, "updates.") && !strings.HasPrefix(field, "dashboard.") && !strings.HasPrefix(field, "alarm.") && !strings.HasPrefix(field, "engine-ecu.") && !strings.HasPrefix(field, "keycard.") && !strings.HasPrefix(field, "pm.") && !strings.HasPrefix(field, "trip.") {
			log.Printf("Warning: Ignoring field '%s' - must be prefixed with 'scooter.', 'cellular.', 'updates.', 'dashboard.', 'alarm.', 'engine-ecu.', 'keycard.', 'pm.', or 'trip.'", field)
		}
	}

	cfg := config.ParseRedisSettings(persisted, s.schema)
	if err := config.SaveToFile(cfg); err != nil {
		return err
	}
	s.lastPersisted = cloneStringMap(persisted)

	log.Printf("Saved %d settings to TOML file (filtered from %d in Redis)", len(persisted), len(settings))

	return nil
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if otherValue, ok := b[key]; !ok || otherValue != value {
			return false
		}
	}
	return true
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func applyTomlOverlay(toml map[string]any, sch *schema.Schema, fields map[string]any, userSet map[string]struct{}) (droppedTransient []string, invalid map[string]string) {
	invalid = make(map[string]string)
	for k, v := range toml {
		// Transient settings are runtime-only and must not survive a restart.
		if sch.IsTransient(k) {
			log.Printf("Ignoring transient key %q from toml", k)
			droppedTransient = append(droppedTransient, k)
			continue
		}
		value := fmt.Sprintf("%v", v)
		if err := sch.ValidateValue(k, value); err != nil {
			log.Printf("Ignoring invalid %s value from toml: %v", k, err)
			invalid[k] = value
			continue
		}
		if err := sch.ValidateTyped(k, value); err != nil {
			log.Printf("Schema type violation for %s value %q from toml (log-only): %v", k, value, err)
		}
		fields[k] = v
		userSet[k] = struct{}{}
	}
	return droppedTransient, invalid
}

func persistedFields(fields map[string]any, userSet map[string]struct{}) map[string]string {
	persisted := make(map[string]string, len(userSet))
	for key := range userSet {
		if value, ok := fields[key]; ok {
			persisted[key] = fmt.Sprintf("%v", value)
		}
	}
	return persisted
}

func transientKeys(sch *schema.Schema) []string {
	if sch == nil {
		return nil
	}
	var out []string
	for k, s := range sch.Settings {
		if s.Transient {
			out = append(out, k)
		}
	}
	return out
}

func filterUserSet(settings map[string]string, userSetKeys map[string]struct{}) map[string]string {
	out := make(map[string]string, len(userSetKeys))
	for k := range userSetKeys {
		if v, ok := settings[k]; ok {
			out[k] = v
		}
		// Indexed records publish their record prefix after all fields are written.
		if isIndexedRecordNotification(k) {
			prefix := k + "."
			for field, value := range settings {
				if strings.HasPrefix(field, prefix) {
					out[field] = value
				}
			}
		}
	}
	return out
}

func isIndexedRecordNotification(key string) bool {
	for _, prefix := range []string{
		"dashboard.saved-locations.",
		"dashboard.recent-destinations.",
		"dashboard.route-plan.",
	} {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		index := strings.TrimPrefix(key, prefix)
		if index == "" {
			return false
		}
		for _, r := range index {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
}

func (s *SettingsService) markUserSet(field string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userSetKeys[field] = struct{}{}
	delete(s.generatedChannelDefaults, field)
}

// WatchSettings subscribes only after boot hydration so the service does not
// mistake its own default notifications for user edits.
func (s *SettingsService) WatchSettings() {
	s.redisClient.Subscribe()
	ch := s.redisClient.WatchChannel()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			switch msg.Channel {
			case redis.SettingsChannel:
				log.Printf("Received update notification for field: %s", msg.Payload)

				if s.consumeGeneratedNotification(msg.Payload) {
					continue
				}
				if s.schema.HasValidation(msg.Payload) && !s.reconcileValidatedSetting(msg.Payload) {
					continue
				}
				if msg.Payload == "dashboard.milestones.mode" {
					pending, exists, err := s.redisClient.GetSettingField("dashboard.milestones.legacy-eggs-pending")
					if err == nil && exists && pending == "true" {
						if err := s.redisClient.SetSettingField("dashboard.milestones.legacy-eggs-pending", "false"); err != nil {
							log.Printf("Error clearing milestone migration marker: %v", err)
						} else {
							s.markUserSet("dashboard.milestones.legacy-eggs-pending")
						}
					}
				}
				s.logTypedViolation(msg.Payload)

				transient := s.schema.IsTransient(msg.Payload)
				overlaid := s.isOverlaid(msg.Payload)
				if overlaid {
					if reassert, isEdit := s.handleOverlaidEdit(msg.Payload, s.currentFieldValue(msg.Payload)); isEdit {
						// Preserve the user edit as the base, then restore the forced value.
						s.markUserSet(msg.Payload)
						if err := s.SaveSettingsToTOML(); err != nil {
							log.Printf("Error saving settings to TOML: %v", err)
						}
						if err := s.redisClient.SetSettingField(msg.Payload, reassert); err != nil {
							log.Printf("Error re-asserting overlay value for %s: %v", msg.Payload, err)
						}
						continue
					}
				}
				if msg.Payload != "" && overlayShouldPersist(transient, overlaid) {
					s.markUserSet(msg.Payload)
				}

				if overlayShouldPersist(transient, overlaid) {
					if err := s.SaveSettingsToTOML(); err != nil {
						log.Printf("Error saving settings to TOML: %v", err)
					}
				}

				if msg.Payload == "cellular.apn" {
					s.updateAPNFromRedis()
				}

				if msg.Payload == "scooter.logserver" {
					s.updateLogServerFromRedis()
				}
			case redis.DashboardChannel:
				if msg.Payload == redis.DashboardReadyField {
					s.refreshChannelDefaultsWhenDashboardReady()
				}
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// logTypedViolation reports declared type and bound violations without
// rejecting the value: format and enum validation are the enforced kinds.
func (s *SettingsService) logTypedViolation(key string) {
	value, exists, err := s.redisClient.GetSettingField(key)
	if err != nil || !exists {
		return
	}
	if err := s.schema.ValidateTyped(key, value); err != nil {
		log.Printf("Schema type violation for %s value %q (log-only): %v", key, value, err)
	}
}

// reconcileValidatedSetting rejects an invalid live value before it reaches
// TOML. The repair is published once; the matching valid notification persists it.
func (s *SettingsService) reconcileValidatedSetting(key string) bool {
	value, exists, err := s.redisClient.GetSettingField(key)
	if err != nil {
		log.Printf("Error reading %s update: %v", key, err)
		return false
	}
	if !exists || s.schema.ValidateValue(key, value) == nil {
		return true
	}

	s.mu.Lock()
	fallback := s.lastPersisted[key]
	s.mu.Unlock()
	if fallback == "" {
		if key == "trip.expunge" {
			fallback = "never"
		} else if defaultValue, ok := s.schema.DefaultValue(key); ok {
			fallback = defaultValue
		}
	}
	if fallback == "" {
		log.Printf("Rejecting invalid live %s value %q without a safe fallback", key, value)
		return false
	}
	log.Printf("Rejecting invalid live %s value %q and restoring %q", key, value, fallback)
	if err := s.redisClient.SetSettingField(key, fallback); err != nil {
		log.Printf("Error restoring %s: %v", key, err)
	}
	return false
}

func (s *SettingsService) updateAPNFromRedis() {
	s.mu.Lock()
	settings, err := s.redisClient.GetAllSettings()
	s.mu.Unlock()
	if err != nil {
		log.Printf("Error getting settings for APN update: %v", err)
		return
	}

	if apn, exists := settings["cellular.apn"]; exists && apn != "" {
		if err := network.UpdateAPN(apn); err != nil {
			log.Printf("Error updating NetworkManager APN: %v", err)
		}
	}
}

func (s *SettingsService) updateLogServerFromRedis() {
	s.mu.Lock()
	settings, err := s.redisClient.GetAllSettings()
	s.mu.Unlock()
	if err != nil {
		log.Printf("Error getting settings for log server update: %v", err)
		return
	}

	if err := journalupload.ApplyLogServer(settings["scooter.logserver"]); err != nil {
		log.Printf("Error applying log server: %v", err)
	}
}

func (s *SettingsService) Close() {
	if s.planServer != nil {
		s.planServer.Stop()
	}
	if s.destServer != nil {
		s.destServer.Stop()
	}
	if s.ipcClient != nil {
		s.ipcClient.Close()
	}
	s.cancel()
	s.redisClient.Close()
}
