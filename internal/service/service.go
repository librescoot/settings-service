package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/librescoot/settings-service/internal/config"
	"github.com/librescoot/settings-service/internal/journalupload"
	"github.com/librescoot/settings-service/internal/network"
	"github.com/librescoot/settings-service/internal/redis"
	"github.com/librescoot/settings-service/internal/schema"
)

type SettingsService struct {
	redisClient *redis.Client
	schema      *schema.Schema
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex

	// userSetKeys tracks settings whose value should be persisted to
	// /data/settings.toml. Keys present in the toml file at load time are
	// user-set; keys that exist in Redis only because of schema.Defaults()
	// are not. Runtime HSETs (lsc, bluetooth-service, ...) add their field
	// to the set when the change notification arrives. Mutated under mu.
	userSetKeys map[string]struct{}
}

// New creates a new settings service instance
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
			log.Printf("Warning: failed to load schema: %v (continuing without schema)", err)
		} else {
			log.Printf("Loaded schema with %d settings", len(s.Settings))
		}
	}

	return &SettingsService{
		redisClient: redisClient,
		schema:      s,
		ctx:         ctx,
		cancel:      cancel,
		userSetKeys: make(map[string]struct{}),
	}, nil
}

// LoadSettingsFromTOML loads settings from TOML file to Redis. Returns
// any error from disk/Redis I/O. If transient keys were stripped from
// toml, the caller (after releasing whatever locks it holds) should
// invoke SaveSettingsToTOML to rewrite the file without them.
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

	// Start with schema defaults
	fields := make(map[string]any)
	if s.schema != nil {
		for k, v := range s.schema.Defaults() {
			fields[k] = v
		}
	}

	// Overlay user settings from TOML (user wins) and track which keys
	// are user-set so SaveSettingsToTOML doesn't bake schema defaults
	// into the toml the next time any setting changes.
	userSet := make(map[string]struct{})
	var droppedTransient []string
	cfg, err := config.LoadFromFile()
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		log.Printf("No %s found, using schema defaults only", config.TomlFilePath)
	} else {
		droppedTransient = applyTomlOverlay(cfg.ToRedisFields(), s.schema, fields, userSet)
	}
	s.userSetKeys = userSet
	rewriteToml = len(droppedTransient) > 0

	// Atomically replace all settings in Redis
	if err := s.redisClient.ReplaceSettings(fields); err != nil {
		return fmt.Errorf("failed to write settings to Redis: %w", err)
	}

	// Redis survives systemctl restarts on this platform, so a previous
	// service instance can have left transient keys in the hash. Clear
	// every declared-transient key that we did NOT just write a fresh
	// value for — keys with a schema default were already populated by
	// ReplaceSettings and must not be wiped here.
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

	// Publish schema to Redis
	if s.schema != nil {
		if err := s.redisClient.SetKey(redis.SchemaKey, string(s.schema.Raw)); err != nil {
			return fmt.Errorf("failed to publish schema to Redis: %w", err)
		}
		log.Printf("Published schema to Redis key %q", redis.SchemaKey)
	}

	// APN and journal-upload reconciliation both shell out to systemctl /
	// NetworkManager and can take many seconds on a cold boot. Run them in
	// the background so LoadSettingsFromTOML returns as soon as Redis is
	// populated — the main goroutine can move on to starting WatchSettings
	// (and unblocks any other service waiting on a populated settings hash).
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

	// journal-upload log server sync. Missing or empty value disables the
	// service so a stale /etc/systemd/journal-upload.conf (e.g. the image
	// default) can't keep shipping to a bogus URL after reboot.
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

// SaveSettingsToTOML saves settings from Redis to TOML file. Only keys
// recorded as user-set (loaded from toml or HSET at runtime) are persisted;
// pure schema defaults stay in Redis but never reach disk.
func (s *SettingsService) SaveSettingsToTOML() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	settings, err := s.redisClient.GetAllSettings()
	if err != nil {
		return fmt.Errorf("failed to get settings from Redis: %w", err)
	}

	log.Printf("Retrieved %d settings from Redis", len(settings))
	for k, v := range settings {
		log.Printf("  %s = %s", k, v)
	}

	// Log any fields that don't match expected patterns
	for field := range settings {
		if !strings.HasPrefix(field, "scooter.") && !strings.HasPrefix(field, "cellular.") && !strings.HasPrefix(field, "updates.") && !strings.HasPrefix(field, "dashboard.") && !strings.HasPrefix(field, "alarm.") && !strings.HasPrefix(field, "engine-ecu.") && !strings.HasPrefix(field, "keycard.") && !strings.HasPrefix(field, "pm.") {
			log.Printf("Warning: Ignoring field '%s' - must be prefixed with 'scooter.', 'cellular.', 'updates.', 'dashboard.', 'alarm.', 'engine-ecu.', 'keycard.', or 'pm.'", field)
		}
	}

	persisted := filterUserSet(settings, s.userSetKeys)

	cfg := config.ParseRedisSettings(persisted)
	if err := config.SaveToFile(cfg); err != nil {
		return err
	}

	log.Printf("Saved %d settings to TOML file (filtered from %d in Redis)", len(persisted), len(settings))

	return nil
}

// applyTomlOverlay merges toml-loaded fields into the boot-time Redis
// hash, marking each persisted key as user-set. Transient keys are
// silently dropped: a stale value in toml (e.g. the legacy
// updates.{mdb,dbc}.channel default that pushed stable scooters onto
// nightly) must not be reloaded into Redis or re-persisted by the next
// SaveSettingsToTOML. Returns the list of transient keys that were
// dropped so the caller can rewrite the toml without them.
func applyTomlOverlay(toml map[string]any, sch *schema.Schema, fields map[string]any, userSet map[string]struct{}) (droppedTransient []string) {
	for k, v := range toml {
		if sch.IsTransient(k) {
			log.Printf("Ignoring transient key %q from toml", k)
			droppedTransient = append(droppedTransient, k)
			continue
		}
		fields[k] = v
		userSet[k] = struct{}{}
	}
	return droppedTransient
}

// transientKeys returns every key declared transient in the schema. Used
// to defensively clear stale values from Redis on load — necessary
// because Redis survives systemctl restarts of settings-service.
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

// filterUserSet returns only the entries from settings whose keys are in
// userSetKeys. Used to keep schema defaults out of the persisted toml.
func filterUserSet(settings map[string]string, userSetKeys map[string]struct{}) map[string]string {
	out := make(map[string]string, len(userSetKeys))
	for k := range userSetKeys {
		if v, ok := settings[k]; ok {
			out[k] = v
		}
	}
	return out
}

// markUserSet records a Redis field as user-set, so it will be included on
// the next SaveSettingsToTOML.
func (s *SettingsService) markUserSet(field string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userSetKeys[field] = struct{}{}
}

// WatchSettings monitors Redis for changes and updates TOML file. The
// subscription is opened here rather than at client construction so the
// initial bulk load from LoadSettingsFromTOML doesn't reach this loop —
// otherwise every schema default would be marked user-set on first boot.
func (s *SettingsService) WatchSettings() {
	s.redisClient.Subscribe()
	ch := s.redisClient.WatchChannel()

	for {
		select {
		case msg := <-ch:
			if msg.Channel == redis.SettingsChannel {
				log.Printf("Received update notification for field: %s", msg.Payload)

				// Every payload on this channel is now a field name —
				// either from a runtime HSET (lsc, bluetooth-service, etc.)
				// or from our own ReplaceSettings (which the Subscribe()
				// ordering above guarantees we don't see).
				//
				// Transient keys live only in Redis: don't mark them
				// user-set and don't rewrite the toml just because one
				// changed.
				transient := s.schema.IsTransient(msg.Payload)
				if msg.Payload != "" && !transient {
					s.markUserSet(msg.Payload)
				}

				if !transient {
					if err := s.SaveSettingsToTOML(); err != nil {
						log.Printf("Error saving settings to TOML: %v", err)
					}
				}

				// Only update NetworkManager if the APN field was changed
				if msg.Payload == "cellular.apn" {
					s.updateAPNFromRedis()
				}

				if msg.Payload == "scooter.logserver" {
					s.updateLogServerFromRedis()
				}
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// updateAPNFromRedis reads the APN from Redis and updates NetworkManager
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

// updateLogServerFromRedis reads scooter.logserver from Redis and
// reconciles the systemd-journal-upload config + service state. Unset or
// empty value disables the service.
func (s *SettingsService) updateLogServerFromRedis() {
	s.mu.Lock()
	settings, err := s.redisClient.GetAllSettings()
	s.mu.Unlock()
	if err != nil {
		log.Printf("Error getting settings for log server update: %v", err)
		return
	}

	// Missing key returns "" from the map, which ApplyLogServer treats as
	// "disable the service" — the behavior we want when the field gets
	// deleted.
	if err := journalupload.ApplyLogServer(settings["scooter.logserver"]); err != nil {
		log.Printf("Error applying log server: %v", err)
	}
}

// Close cleanly shuts down the service
func (s *SettingsService) Close() {
	s.cancel()
	s.redisClient.Close()
}
