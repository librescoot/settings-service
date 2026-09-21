package service

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/librescoot/settings-service/internal/redis"
	"github.com/librescoot/settings-service/internal/schema"
)

const defaultOSReleasePath = "/etc/os-release"

func readOSReleaseVersion(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "VERSION_ID" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if value == "" {
			return "", fmt.Errorf("VERSION_ID is empty in %s", path)
		}
		return value, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("VERSION_ID not found in %s", path)
}

func (s *SettingsService) applyInitialChannelDefaults(fields map[string]any, userSet map[string]struct{}) {
	s.localReleaseChannel = ""
	s.generatedChannelDefaults = make(map[string]string)
	s.pendingGeneratedNotifications = make(map[string]string)

	version, err := readOSReleaseVersion(s.osReleasePath)
	if err != nil {
		log.Printf("Unable to read local release channel: %v", err)
	} else if channel := schema.ReleaseChannel(version); channel != "" {
		s.localReleaseChannel = channel
	} else {
		log.Printf("Unable to infer local release channel from VERSION_ID %q", version)
	}

	for key, value := range s.selectedChannelDefaults() {
		if _, isUserSet := userSet[key]; !isUserSet {
			fields[key] = value
		}
	}
	if s.schema == nil {
		return
	}
	for key, setting := range s.schema.Settings {
		if len(setting.ChannelDefaults) == 0 {
			continue
		}
		if _, isUserSet := userSet[key]; isUserSet {
			continue
		}
		if value, exists := fields[key]; exists {
			s.generatedChannelDefaults[key] = fmt.Sprintf("%v", value)
		}
	}
}

func (s *SettingsService) selectedChannelDefaults() map[string]string {
	if s.schema == nil {
		return nil
	}

	channels := make([]string, 0, 2)
	if s.localReleaseChannel != "" {
		channels = append(channels, s.localReleaseChannel)
	}
	version, exists, err := s.redisClient.GetHashField("version:dbc", "version_id")
	if err != nil {
		log.Printf("Unable to read DBC installed version: %v", err)
	} else if exists {
		if channel := schema.ReleaseChannel(version); channel != "" {
			channels = append(channels, channel)
		}
	}
	return s.schema.ChannelDefaults(channels)
}

func (s *SettingsService) refreshChannelDefaultsWhenDashboardReady() {
	ready, exists, err := s.redisClient.GetHashField(redis.DashboardKey, redis.DashboardReadyField)
	if err != nil {
		log.Printf("Unable to read dashboard readiness: %v", err)
		return
	}
	if exists && ready == "true" {
		s.refreshChannelDefaults()
	}
}

func (s *SettingsService) refreshChannelDefaults() {
	selected := s.selectedChannelDefaults()
	if len(selected) == 0 {
		return
	}

	s.mu.Lock()
	changed := false
	for key, value := range selected {
		_, isUserSet := s.userSetKeys[key]
		generated, tracked := s.generatedChannelDefaults[key]
		if !isUserSet && tracked && generated != value {
			changed = true
			break
		}
	}
	s.mu.Unlock()
	if !changed {
		return
	}

	current, err := s.redisClient.GetAllSettings()
	if err != nil {
		log.Printf("Unable to refresh channel-dependent defaults: %v", err)
		return
	}

	type update struct {
		key      string
		previous string
		value    string
	}
	updates := make([]update, 0, len(selected))

	s.mu.Lock()
	for key, value := range selected {
		if _, isUserSet := s.userSetKeys[key]; isUserSet {
			continue
		}
		previous, generated := s.generatedChannelDefaults[key]
		if !generated || current[key] != previous || current[key] == value {
			continue
		}
		s.generatedChannelDefaults[key] = value
		s.pendingGeneratedNotifications[key] = value
		updates = append(updates, update{key: key, previous: previous, value: value})
	}
	s.mu.Unlock()

	for _, update := range updates {
		if err := s.redisClient.SetSettingField(update.key, update.value); err == nil {
			continue
		} else {
			log.Printf("Unable to update channel-dependent default %s: %v", update.key, err)
		}
		s.mu.Lock()
		if s.pendingGeneratedNotifications[update.key] == update.value {
			delete(s.pendingGeneratedNotifications, update.key)
		}
		if s.generatedChannelDefaults[update.key] == update.value {
			s.generatedChannelDefaults[update.key] = update.previous
		}
		s.mu.Unlock()
	}
}

func (s *SettingsService) consumeGeneratedNotification(field string) bool {
	s.mu.Lock()
	expected, pending := s.pendingGeneratedNotifications[field]
	if pending {
		delete(s.pendingGeneratedNotifications, field)
	}
	s.mu.Unlock()
	if !pending {
		return false
	}

	value, exists, err := s.redisClient.GetSettingField(field)
	if err != nil {
		log.Printf("Unable to verify generated default %s: %v", field, err)
		return true
	}
	return exists && value == expected
}
