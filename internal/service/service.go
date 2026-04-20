package service

import (
	"context"
	"fmt"
	"log"
	"os"
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
	}, nil
}

// LoadSettingsFromTOML loads settings from TOML file to Redis
func (s *SettingsService) LoadSettingsFromTOML() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Start with schema defaults
	fields := make(map[string]any)
	if s.schema != nil {
		for k, v := range s.schema.Defaults() {
			fields[k] = v
		}
	}

	// Overlay user settings from TOML (user wins)
	cfg, err := config.LoadFromFile()
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		log.Printf("No %s found, using schema defaults only", config.TomlFilePath)
	} else {
		for k, v := range cfg.ToRedisFields() {
			fields[k] = v
		}
	}

	// Atomically replace all settings in Redis
	if err := s.redisClient.ReplaceSettings(fields); err != nil {
		return fmt.Errorf("failed to write settings to Redis: %w", err)
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

	// APN sync
	if apn, ok := fields["cellular.apn"]; ok && apn != "" {
		currentAPN, err := network.GetCurrentAPN()
		if err != nil {
			log.Printf("Error reading current APN: %v", err)
		} else if currentAPN != fmt.Sprintf("%v", apn) {
			log.Printf("APN mismatch detected: NetworkManager has '%s', settings have '%v'", currentAPN, apn)
			if err := network.UpdateAPN(fmt.Sprintf("%v", apn)); err != nil {
				log.Printf("Error updating NetworkManager APN on startup: %v", err)
			}
		} else {
			log.Printf("APN is already synchronized: %v", apn)
		}
	}

	// journal-upload log server sync. Only act when the key exists; if it's
	// missing from settings, we leave the service state alone.
	if logserver, ok := fields["scooter.logserver"]; ok {
		if err := journalupload.ApplyLogServer(fmt.Sprintf("%v", logserver)); err != nil {
			log.Printf("Error applying log server on startup: %v", err)
		}
	}

	return nil
}

// SaveSettingsToTOML saves settings from Redis to TOML file
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

	// Warn about fields that the schema doesn't declare. Only runs when a
	// schema is loaded; unknown fields are still persisted.
	if s.schema != nil {
		for field := range settings {
			if !s.schema.Has(field) {
				log.Printf("Warning: field '%s' is not declared in the schema", field)
			}
		}
	}

	// Parse settings and save to file
	cfg := config.ParseRedisSettings(settings)
	if err := config.SaveToFile(cfg); err != nil {
		return err
	}

	log.Printf("Saved %d settings to TOML file", len(settings))

	return nil
}

// WatchSettings monitors Redis for changes and updates TOML file
func (s *SettingsService) WatchSettings() {
	ch := s.redisClient.WatchChannel()

	for {
		select {
		case msg := <-ch:
			if msg.Channel == redis.SettingsChannel {
				log.Printf("Received update notification for field: %s", msg.Payload)

				if err := s.SaveSettingsToTOML(); err != nil {
					log.Printf("Error saving settings to TOML: %v", err)
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
