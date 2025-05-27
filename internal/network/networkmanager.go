package network

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

const NMConnectionPath = "/etc/NetworkManager/system-connections/wwan.nmconnection"

// GetCurrentAPN reads the current APN from NetworkManager configuration
func GetCurrentAPN() (string, error) {
	if _, err := os.Stat(NMConnectionPath); os.IsNotExist(err) {
		return "", nil
	}

	content, err := os.ReadFile(NMConnectionPath)
	if err != nil {
		return "", fmt.Errorf("failed to read NetworkManager connection file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	inGsmSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		if trimmed == "[gsm]" {
			inGsmSection = true
			continue
		}
		
		if inGsmSection && strings.HasPrefix(trimmed, "[") {
			break
		}
		
		if inGsmSection && strings.HasPrefix(trimmed, "apn=") {
			return strings.TrimPrefix(trimmed, "apn="), nil
		}
	}

	return "", nil
}

// UpdateAPN updates the APN in the NetworkManager connection file
func UpdateAPN(apn string) error {
	// Check if the file exists
	if _, err := os.Stat(NMConnectionPath); os.IsNotExist(err) {
		log.Printf("NetworkManager connection file %s does not exist, skipping APN update", NMConnectionPath)
		return nil
	}

	// Read the existing file
	content, err := os.ReadFile(NMConnectionPath)
	if err != nil {
		return fmt.Errorf("failed to read NetworkManager connection file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	updated := false
	inGsmSection := false

	// Update the APN line
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		
		// Check if we're entering the [gsm] section
		if trimmed == "[gsm]" {
			inGsmSection = true
			continue
		}
		
		// Check if we're leaving the [gsm] section
		if inGsmSection && strings.HasPrefix(trimmed, "[") {
			inGsmSection = false
			continue
		}
		
		// Update the APN line if we're in the [gsm] section
		if inGsmSection && strings.HasPrefix(trimmed, "apn=") {
			lines[i] = fmt.Sprintf("apn=%s", apn)
			updated = true
		}
	}

	if !updated {
		log.Printf("APN line not found in NetworkManager connection file")
		return nil
	}

	// Write the updated content back
	updatedContent := strings.Join(lines, "\n")
	if err := os.WriteFile(NMConnectionPath, []byte(updatedContent), 0600); err != nil {
		return fmt.Errorf("failed to write NetworkManager connection file: %w", err)
	}

	log.Printf("Updated NetworkManager APN to: %s", apn)

	// Restart NetworkManager to apply changes
	log.Println("Restarting NetworkManager to apply APN changes...")
	cmd := exec.Command("systemctl", "restart", "NetworkManager")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to restart NetworkManager: %w", err)
	}
	log.Println("NetworkManager restarted successfully")

	return nil
}