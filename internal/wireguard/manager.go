package wireguard

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	WireGuardConfigDir = "/data/wireguard"
	DefaultTimeout     = 120 * time.Second
	SettlingDelay      = 3 * time.Second
	PollInterval       = 2 * time.Second
)

// Manager handles WireGuard connection lifecycle
type Manager struct {
	configDir   string
	timeout     time.Duration
	redisClient *redis.Client
}

// NewManager creates a new WireGuard manager with a Redis client for internet detection
func NewManager(redisClient *redis.Client) *Manager {
	return &Manager{
		configDir:   WireGuardConfigDir,
		timeout:     DefaultTimeout,
		redisClient: redisClient,
	}
}

// NewManagerWithOptions creates a manager with custom options (useful for testing)
func NewManagerWithOptions(configDir string, timeout time.Duration, redisClient *redis.Client) *Manager {
	return &Manager{
		configDir:   configDir,
		timeout:     timeout,
		redisClient: redisClient,
	}
}

// Initialize performs the WireGuard initialization sequence
func (m *Manager) Initialize(ctx context.Context) error {
	log.Println("Starting WireGuard initialization...")

	if err := m.deleteExistingConnections(); err != nil {
		return fmt.Errorf("failed to delete existing connections: %w", err)
	}

	shouldImport, err := m.shouldImportConfigs()
	if err != nil {
		log.Printf("Error checking for WireGuard configs: %v", err)
	}

	if !shouldImport {
		log.Println("No WireGuard configurations to import, all connections have been removed")
		return nil
	}

	if err := m.waitForInternet(ctx); err != nil {
		return fmt.Errorf("internet wait cancelled: %w", err)
	}

	if err := m.importConfigurations(); err != nil {
		return fmt.Errorf("failed to import configurations: %w", err)
	}

	log.Println("WireGuard initialization completed")
	return nil
}

// waitForInternet waits for internet connectivity via Redis, falling back to a timeout
func (m *Manager) waitForInternet(ctx context.Context) error {
	if m.redisClient == nil {
		log.Printf("No Redis client, waiting %v before importing WireGuard configurations...", m.timeout)
		select {
		case <-time.After(m.timeout):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if m.isInternetConnected(ctx) {
		log.Println("Internet already connected, settling before WireGuard import...")
		select {
		case <-time.After(SettlingDelay):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	log.Printf("Waiting for internet connectivity (timeout %v)...", m.timeout)

	pubsub := m.redisClient.Subscribe(ctx, "internet")
	defer pubsub.Close()

	msgCh := pubsub.Channel()
	pollTicker := time.NewTicker(PollInterval)
	defer pollTicker.Stop()
	timeoutTimer := time.NewTimer(m.timeout)
	defer timeoutTimer.Stop()

	for {
		select {
		case msg := <-msgCh:
			if msg != nil && msg.Payload == "status" {
				if m.isInternetConnected(ctx) {
					log.Println("Internet connected (via pub/sub), settling before WireGuard import...")
					select {
					case <-time.After(SettlingDelay):
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
		case <-pollTicker.C:
			if m.isInternetConnected(ctx) {
				log.Println("Internet connected (via poll), settling before WireGuard import...")
				select {
				case <-time.After(SettlingDelay):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		case <-timeoutTimer.C:
			log.Printf("Internet wait timed out after %v, proceeding with WireGuard import anyway", m.timeout)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// isInternetConnected checks the Redis internet hash for connected status
func (m *Manager) isInternetConnected(ctx context.Context) bool {
	status, err := m.redisClient.HGet(ctx, "internet", "status").Result()
	if err != nil {
		return false
	}
	return status == "connected"
}

// shouldImportConfigs checks if there are config files to import
func (m *Manager) shouldImportConfigs() (bool, error) {
	if _, err := os.Stat(m.configDir); os.IsNotExist(err) {
		return false, nil
	}

	pattern := filepath.Join(m.configDir, "*.conf")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return false, err
	}

	return len(files) > 0, nil
}

// deleteExistingConnections removes all existing WireGuard connections
func (m *Manager) deleteExistingConnections() error {
	cmd := exec.Command("nmcli", "-t", "-f", "NAME,TYPE", "con", "show")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list connections: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	deletedCount := 0
	var failed []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) >= 2 && parts[1] == "wireguard" {
			connName := parts[0]
			log.Printf("Deleting WireGuard connection: %s", connName)

			cmd := exec.Command("nmcli", "con", "delete", connName)
			if err := cmd.Run(); err != nil {
				log.Printf("Warning: Failed to delete connection %s: %v", connName, err)
				failed = append(failed, connName)
			} else {
				deletedCount++
			}
		}
	}

	log.Printf("Deleted %d WireGuard connections", deletedCount)
	if len(failed) > 0 {
		return fmt.Errorf("failed to delete %d connections: %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

// importConfigurations imports all WireGuard config files from the config directory
func (m *Manager) importConfigurations() error {
	if _, err := os.Stat(m.configDir); os.IsNotExist(err) {
		log.Printf("WireGuard config directory %s does not exist, skipping import", m.configDir)
		return nil
	}

	pattern := filepath.Join(m.configDir, "*.conf")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("failed to list config files: %w", err)
	}

	if len(files) == 0 {
		log.Printf("No WireGuard configuration files found in %s", m.configDir)
		return nil
	}

	importedCount := 0
	for _, file := range files {
		log.Printf("Importing WireGuard configuration: %s", filepath.Base(file))

		cmd := exec.Command("nmcli", "connection", "import", "type", "wireguard", "file", file)
		if err := cmd.Run(); err != nil {
			log.Printf("Warning: Failed to import %s: %v", file, err)
		} else {
			importedCount++
		}
	}

	log.Printf("Imported %d WireGuard configurations", importedCount)
	return nil
}
