package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/librescoot/settings-service/internal/config"
	"github.com/librescoot/settings-service/internal/network"
	"github.com/librescoot/settings-service/internal/redis"
)

type SettingsService struct {
	redisClient *redis.Client
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
}

// New creates a new settings service instance
func New(redisAddr string) (*SettingsService, error) {
	ctx, cancel := context.WithCancel(context.Background())

	redisClient, err := redis.NewClient(ctx, redisAddr)
	if err != nil {
		cancel()
		return nil, err
	}

	return &SettingsService{
		redisClient: redisClient,
		ctx:         ctx,
		cancel:      cancel,
	}, nil
}

// LoadSettingsFromTOML loads settings from TOML file to Redis
func (s *SettingsService) LoadSettingsFromTOML() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Always flush existing settings in Redis first
	log.Println("Flushing existing settings in Redis")
	if err := s.redisClient.FlushSettings(); err != nil {
		return fmt.Errorf("failed to flush settings: %w", err)
	}

	// Load configuration from file
	cfg, err := config.LoadFromFile()
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("TOML file %s does not exist, Redis settings cleared", config.TomlFilePath)
			return nil
		}
		return err
	}

	// If scooter section is nil or empty, we already flushed Redis
	if cfg.Scooter == nil || len(cfg.Scooter) == 0 {
		log.Println("Empty or missing [scooter] section, Redis settings remain cleared")
	}

	// Convert to Redis fields and save
	fields := cfg.ToRedisFields()
	if len(fields) > 0 {
		if err := s.redisClient.SetSettings(fields); err != nil {
			return fmt.Errorf("failed to write settings to Redis: %w", err)
		}
	}

	log.Printf("Loaded %d settings from TOML file to Redis", len(fields))

	// Check if APN needs to be synchronized on startup
	if apn, exists := cfg.Cellular["apn"]; exists && apn != "" {
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

	// Log any fields that don't match expected patterns
	for field := range settings {
		if !strings.HasPrefix(field, "scooter.") && !strings.HasPrefix(field, "cellular.") {
			log.Printf("Warning: Ignoring field '%s' - must be prefixed with 'scooter.' or 'cellular.'", field)
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
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// updateAPNFromRedis reads the APN from Redis and updates NetworkManager
func (s *SettingsService) updateAPNFromRedis() {
	settings, err := s.redisClient.GetAllSettings()
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

// Close cleanly shuts down the service
func (s *SettingsService) Close() {
	s.cancel()
	s.redisClient.Close()
}