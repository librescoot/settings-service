package wireguard

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	WireGuardConfigDir = "/data/wireguard"
	StartupDelay      = 120 * time.Second
)

// Manager handles WireGuard connection lifecycle
type Manager struct {
	configDir string
	delay     time.Duration
}

// NewManager creates a new WireGuard manager
func NewManager() *Manager {
	return &Manager{
		configDir: WireGuardConfigDir,
		delay:     StartupDelay,
	}
}

// NewManagerWithOptions creates a manager with custom options (useful for testing)
func NewManagerWithOptions(configDir string, delay time.Duration) *Manager {
	return &Manager{
		configDir: configDir,
		delay:     delay,
	}
}

// Initialize performs the WireGuard initialization sequence
func (m *Manager) Initialize() error {
	log.Println("Starting WireGuard initialization...")

	// Always delete existing WireGuard connections first
	if err := m.deleteExistingConnections(); err != nil {
		return fmt.Errorf("failed to delete existing connections: %w", err)
	}

	// Check if we should import new configurations
	shouldImport, err := m.shouldImportConfigs()
	if err != nil {
		log.Printf("Error checking for WireGuard configs: %v", err)
	}

	if !shouldImport {
		log.Println("No WireGuard configurations to import, all connections have been removed")
		return nil
	}

	// Wait before importing new connections
	log.Printf("Waiting %v before importing WireGuard configurations...", m.delay)
	time.Sleep(m.delay)

	// Import new connections
	if err := m.importConfigurations(); err != nil {
		return fmt.Errorf("failed to import configurations: %w", err)
	}

	log.Println("WireGuard initialization completed")
	return nil
}

// shouldImportConfigs checks if there are config files to import
func (m *Manager) shouldImportConfigs() (bool, error) {
	// Check if directory exists
	if _, err := os.Stat(m.configDir); os.IsNotExist(err) {
		return false, nil
	}

	// Check for .conf files
	pattern := filepath.Join(m.configDir, "*.conf")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return false, err
	}

	return len(files) > 0, nil
}

// deleteExistingConnections removes all existing WireGuard connections
func (m *Manager) deleteExistingConnections() error {
	// Get list of WireGuard connections
	cmd := exec.Command("nmcli", "-t", "-f", "NAME,TYPE", "con", "show")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list connections: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	deletedCount := 0

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
			} else {
				deletedCount++
			}
		}
	}

	log.Printf("Deleted %d WireGuard connections", deletedCount)
	return nil
}

// importConfigurations imports all WireGuard config files from the config directory
func (m *Manager) importConfigurations() error {
	// Check if directory exists
	if _, err := os.Stat(m.configDir); os.IsNotExist(err) {
		log.Printf("WireGuard config directory %s does not exist, skipping import", m.configDir)
		return nil
	}

	// Find all .conf files
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