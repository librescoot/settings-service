package network

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/librescoot/settings-service/internal/fileutil"
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
	gsmSectionIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "[gsm]" {
			inGsmSection = true
			gsmSectionIdx = i
			continue
		}

		if inGsmSection && strings.HasPrefix(trimmed, "[") {
			if !updated {
				// [gsm] section exists but has no apn= line; insert before this section header
				newLine := fmt.Sprintf("apn=%s", apn)
				lines = append(lines[:i], append([]string{newLine}, lines[i:]...)...)
				updated = true
			}
			inGsmSection = false
			continue
		}

		if inGsmSection && strings.HasPrefix(trimmed, "apn=") {
			lines[i] = fmt.Sprintf("apn=%s", apn)
			updated = true
		}
	}

	// If we reached EOF while still in the [gsm] section without finding apn=
	if inGsmSection && !updated {
		lines = append(lines, fmt.Sprintf("apn=%s", apn))
		updated = true
	}

	// No [gsm] section at all — append one
	if gsmSectionIdx == -1 {
		lines = append(lines, "", "[gsm]", fmt.Sprintf("apn=%s", apn))
		updated = true
	}

	if !updated {
		return nil
	}

	updatedContent := strings.Join(lines, "\n")
	if err := fileutil.AtomicWrite(NMConnectionPath, 0600, func(f *os.File) error {
		_, err := f.WriteString(updatedContent)
		return err
	}); err != nil {
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
